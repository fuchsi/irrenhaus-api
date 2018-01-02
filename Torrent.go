/*
 * irrenhaus-api, API wrapper for irrenhaus.dyndns.dk
 * Copyright (C) 2018  Daniel Müller
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>
 */

package irrenhaus_api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/c2h5oh/datasize"
	"github.com/fuchsi/irrenhaus-api/Category"
	"golang.org/x/net/html"
)

type TorrentUpload struct {
	c *Connection

	Meta        io.Reader
	Nfo         io.Reader
	Image1      io.Reader
	Image2      io.Reader
	Name        string
	Description string
	Category    int

	Id int64
}

type TorrentEntry struct {
	Id           int
	Name         string
	Category     int
	Added        time.Time
	Size         uint64
	Description  string
	InfoHash     string
	FileCount    int
	SeederCount  int
	LeecherCount int
	SnatchCount  int
	CommentCount int
	Uploader     string

	Files    []TorrentFile
	Peers    []Peer
	Snatches []Snatch
}

type TorrentFile struct {
	Name string
	Size uint64
}

type Peer struct {
	Name        string
	Connectable bool
	Seeder      bool
	Uploaded    uint64
	Downloaded  uint64
	Ulrate      uint64
	Dlrate      uint64
	Ratio       float64
	Completed   float64
	Connected   uint64
	Idle        uint64
	Client      string
}

type Snatch struct {
	Name       string
	Uploaded   uint64
	Downloaded uint64
	Ratio      float64
	Completed  time.Time
	Stopped    time.Time
	Seeding    bool
}

type TorrentList struct {
	Page    int64
	Entries []TorrentEntry
}

func DownloadTorrent(c *Connection, id int64) ([]byte, string, error) {
	if err := c.assureLogin(); err != nil {
		return nil, "", err
	}
	resp, err := c.get(c.buildUrl("/download.php", url.Values{"torrent": {fmt.Sprintf("%d", id)}}))
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	debugRequest(resp, string(body))

	if resp.StatusCode == 404 {
		return nil, "", errors.New("torrent not found")
	}

	filename := resp.Header.Get("Content-Disposition")
	re, _ := regexp.Compile(`^attachment; filename="(.+)"$`)
	if re.MatchString(filename) {
		filename = re.FindStringSubmatch(filename)[1]
	}

	return body, filename, nil
}

func NewUpload(c *Connection, meta io.Reader, nfo io.Reader, image io.Reader, name string, category int, description string) (TorrentUpload, error) {
	t := TorrentUpload{
		Meta:        meta,
		Nfo:         nfo,
		Image1:      image,
		Name:        name,
		Category:    category,
		Description: description,
		c:           c,
	}

	return t, nil
}

func (t *TorrentUpload) Upload() (error) {
	if err := t.c.assureLogin(); err != nil {
		return err
	}

	bodyBuf := &bytes.Buffer{}
	bodyWriter := multipart.NewWriter(bodyBuf)

	bodyWriter.WriteField("name", t.Name)
	bodyWriter.WriteField("type", fmt.Sprintf("%d", t.Category))
	bodyWriter.WriteField("descr", t.Description)

	metaWriter, err := bodyWriter.CreateFormFile("file", t.Name+".torrent")
	if err != nil {
		fmt.Println("error writing to buffer")
		return err
	}
	_, err = io.Copy(metaWriter, t.Meta)
	if err != nil {
		return err
	}

	nfoWriter, err := bodyWriter.CreateFormFile("nfo", t.Name+".nfo")
	if err != nil {
		fmt.Println("error writing to buffer")
		return err
	}
	_, err = io.Copy(nfoWriter, t.Nfo)
	if err != nil {
		return err
	}

	image1Writer, err := bodyWriter.CreateFormFile("pic1", t.Name+".jpg")
	if err != nil {
		fmt.Println("error writing to buffer")
		return err
	}
	_, err = io.Copy(image1Writer, t.Image1)
	if err != nil {
		return err
	}

	if t.Image2 != nil {
		image2Writer, err := bodyWriter.CreateFormFile("pic1", t.Name+"_2"+".jpg")
		if err != nil {
			fmt.Println("error writing to buffer")
			return err
		}
		_, err = io.Copy(image2Writer, t.Image2)
		if err != nil {
			return err
		}
	}

	contentType := bodyWriter.FormDataContentType()
	bodyWriter.Close()

	resp, err := t.c.post(t.c.buildUrl("takeupload.php", nil), contentType, bodyBuf)

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	sbody := string(body)
	debugRequest(resp, sbody)

	if resp.StatusCode == 404 {
		return errors.New("upload failed: 404")
	}

	if strings.Contains(sbody, "TorrentUpload-Upload fehlgeschlagen!") {
		errorMsg := "unknown error"
		re, _ := regexp.Compile("Beim Upload ist ein schwerwiegender Fehler aufgetreten:</p><p.*>(.+)</p>")
		if re.MatchString(sbody) {
			errorMsg = re.FindStringSubmatch(sbody)[1]
		}
		return errors.New("upload failed: " + errorMsg)
	}

	re, _ := regexp.Compile("<a href=\"details\\.php\\?id=(\\d+)\">Weiter zu den Details Deines Torrents</a>")
	if re.MatchString(sbody) {
		t.Id, err = strconv.ParseInt(re.FindStringSubmatch(sbody)[1], 10, 64)
		if err != nil {
			return err
		}
	}

	return nil
}

func Search(c *Connection, needle string, categories []int, dead bool) ([]TorrentEntry, error) {
	if err := c.assureLogin(); err != nil {
		return nil, err
	}
	deadint := 0
	if dead {
		deadint = 1
	}
	data := url.Values{"search": {needle}, "incldead": {fmt.Sprintf("%d", deadint)}, "orderby": {"added"}}
	if len(categories) == 1 {
		data.Add("cat", fmt.Sprintf("%d", categories[0]))
	} else {
		for _, cat := range categories {
			data.Add(fmt.Sprintf("c%d", cat), "1")
		}
	}
	resp, err := c.get(c.buildUrl("/browse.php", data))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	debugRequest(resp, string(body))

	foundTorrents := make(map[int]TorrentEntry)
	torrentList := make([]TorrentEntry, len(foundTorrents))
	maxpage := int64(0)
	chTorrents := make(chan TorrentEntry)
	chFinished := make(chan bool)

	reader := bytes.NewReader(body)
	go func(reader io.Reader, chTorrents chan TorrentEntry, chFinished chan bool) {
		defer func() {
			// Notify that we're done after this function
			chFinished <- true
		}()
		parseTorrentList(reader, chTorrents)
	}(reader, chTorrents, chFinished)

	re, _ := regexp.Compile("<a href=\"(.+&page=(\\d+))\".*>")
	if re.MatchString(string(body)) {
		matches := re.FindAllStringSubmatch(string(body), -1)
		for _, m := range matches {
			page, _ := strconv.ParseInt(m[2], 10, 32)
			if page > maxpage {
				maxpage = page
			}
		}

		//fmt.Println("Pages: ", maxpage)

		for p := int64(1); p <= maxpage; p++ {
			data.Set("page", fmt.Sprintf("%d", p))
			pageUrl := c.buildUrl("/browse.php", data)
			go crawlTorrentList(c, pageUrl, p, chTorrents, chFinished)
		}
	}

	for p := int64(0); p <= maxpage; {
		select {
		case torrent := <-chTorrents:
			foundTorrents[torrent.Id] = torrent
			//fmt.Println("found torrent:", torrent.Id)
		case <-chFinished:
			p++
			//fmt.Println("finished a parser. now at", p, "of", maxpage)
		}
	}

	close(chFinished)
	close(chTorrents)

	for _, torrent := range foundTorrents {
		torrentList = append(torrentList, torrent)
	}

	return torrentList, nil
}

func crawlTorrentList(c *Connection, url string, page int64, chTorrents chan TorrentEntry, chFinished chan bool) {
	resp, err := c.get(url)
	//fmt.Println("Crawl Page:", page)

	defer func() {
		// Notify that we're done after this function
		chFinished <- true
	}()

	if err != nil {
		fmt.Println("ERROR: Failed to crawl \"" + url + "\"")
		return
	}

	b := resp.Body
	defer b.Close() // close Body when the function returns

	parseTorrentList(b, chTorrents)
}

func parseTorrentList(body io.Reader, ch chan TorrentEntry) {
	//fmt.Println("Parsing Torrent List")
	z := html.NewTokenizer(body)
	isInTorrentTable := false
	isInTorrentEntry := false
	checkIfNextIsTyp := false

	for {
		tt := z.Next()
		//fmt.Println("tt:", tt)

		switch {
		case tt == html.ErrorToken:
			// End of the document, we're done
			return
		case tt == html.StartTagToken:
			t := z.Token()

			if !isInTorrentTable && !checkIfNextIsTyp {
				// Check if the token is an <td> tag
				isTd := t.Data == "td"
				//fmt.Println("t.Data =", t.Data, "isTd:", isTd)
				if !isTd {
					continue
				}

				if !isInTorrentEntry {
					// Check if the css class is "tablecat"
					ok, class := getCssClass(t)
					if !ok {
						continue
					}
					if class == "tablecat" {
						//fmt.Println("found <td class=\"tablecat\">")
						checkIfNextIsTyp = true
					}
				}
			} else if isInTorrentTable {
				if t.Data == "tr" {
					//fmt.Println("In Torrent Table. Found <tr>")
					isInTorrentEntry = true
					torrentEntry, err := parseTorrentEntry(z)
					if err != nil {
						fmt.Println("ERROR while parsing the torrent entry:", err.Error())
					}
					//fmt.Println(torrentEntry)
					ch <- torrentEntry
				}
			}
		case tt == html.EndTagToken:
			t := z.Token()
			if isInTorrentTable {
				if t.Data == "table" {
					isInTorrentTable = false
				}
				if isInTorrentEntry {
					if t.Data == "tr" {
						isInTorrentEntry = false
					}
				}
			}
		case tt == html.TextToken:
			t := z.Token()
			if checkIfNextIsTyp {
				if t.Data == "Typ" {
					//fmt.Println("Found 'Typ'. Now in Torrent Table")
					checkIfNextIsTyp = false
					isInTorrentTable = true
				}
			}
		}
	}
}

func parseTorrentEntry(z *html.Tokenizer) (TorrentEntry, error) {
	te := TorrentEntry{}
	//fmt.Println("Parsing Torrent Entry")

	z.Next() // typ td
	z.Next() // typ anchor

	// Category
	t := z.Token()
	ok, href := getAttr(t, "href")
	if !ok {
		return te, errors.New("typ is missing href attr")
	}
	cre, _ := regexp.Compile("browse\\.php\\?cat=(\\d+)")
	if cre.MatchString(href) {
		cat, err := strconv.ParseInt(cre.FindStringSubmatch(href)[1], 10, 32)
		if err != nil {
			return te, err
		}
		te.Category = int(cat)
	}

	// loop until next anchor tag
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.StartTagToken && t.Data == "a" {
			break
		}
	}

	// ID
	ok, href = getAttr(t, "href")
	if !ok {
		return te, errors.New("name is missing href attr")
	}
	ire, _ := regexp.Compile("details\\.php\\?id=(\\d+)")
	if ire.MatchString(href) {
		id, err := strconv.ParseInt(ire.FindStringSubmatch(href)[1], 10, 32)
		if err != nil {
			return te, err
		}
		te.Id = int(id)
	}

	z.Next() // name b
	z.Next() // name text

	// Name
	t = z.Token()
	te.Name = t.Data

	// loop until next anchor tag
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.StartTagToken && t.Data == "a" {
			break
		}
	}

	// Files
	z.Next()
	t = z.Token()
	files, err := strconv.ParseInt(t.Data, 10, 32)
	if err != nil {
		return te, err
	}
	te.FileCount = int(files)

	// loop until next anchor tag
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.StartTagToken && t.Data == "a" {
			break
		}
	}

	// Comments
	z.Next()
	t = z.Token()
	comments, err := strconv.ParseInt(t.Data, 10, 32)
	if err != nil {
		return te, err
	}
	te.CommentCount = int(comments)

	// loop until next td tag
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.StartTagToken && t.Data == "td" {
			break
		}
	}

	// Added date/time
	z.Next() // Date
	t = z.Token()
	adt := t.Data
	z.Next() // br
	z.Next() // Time
	t = z.Token()
	adt += " " + t.Data
	te.Added, err = time.Parse("02.01.2006 15:04:05", adt)
	if err != nil {
		return te, err
	}

	// loop until next td tag
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.StartTagToken && t.Data == "td" {
			break
		}
	}
	// loop until next td tag
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.StartTagToken && t.Data == "td" {
			break
		}
	}

	// Size
	z.Next()
	t = z.Token()
	commaIndex := strings.IndexByte(t.Data, ',')
	// get the part before the ','
	size, err := strconv.ParseInt(t.Data[0:commaIndex], 10, 32)
	if err != nil {
		return te, err
	}
	// part after the ','
	size2, err := strconv.ParseInt(t.Data[(commaIndex + 1):], 10, 32)
	if err != nil {
		return te, err
	}
	// combine both
	size *= 100
	size += size2
	realsize := float64(size) / 100

	z.Next() // br
	z.Next() // size suffix
	t = z.Token()

	switch t.Data {
	case "KB":
		realsize *= float64(datasize.KB)
	case "MB":
		realsize *= float64(datasize.MB)
	case "GB":
		realsize *= float64(datasize.GB)
	case "TB":
		realsize *= float64(datasize.TB)
	case "PB":
		realsize *= float64(datasize.PB)
	case "EB":
		realsize *= float64(datasize.EB)
	}
	te.Size = uint64(realsize)

	// loop until next anchor tag
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.StartTagToken && t.Data == "a" {
			break
		}
	}

	// Snatch Count
	z.Next() // text in a
	t = z.Token()
	snatches, err := strconv.ParseInt(t.Data, 10, 32)
	if err != nil {
		return te, err
	}
	te.SnatchCount = int(snatches)

	// loop until next font tag
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.StartTagToken && t.Data == "font" {
			break
		}
	}

	// Seeder Count
	z.Next() // text in font
	t = z.Token()
	seeders, err := strconv.ParseInt(t.Data, 10, 32)
	if err != nil {
		return te, err
	}
	te.SeederCount = int(seeders)

	// loop until next font tag
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.StartTagToken && t.Data == "font" {
			break
		}
	}

	// Leecher Count
	z.Next() // text in font
	t = z.Token()
	leechers, err := strconv.ParseInt(t.Data, 10, 32)
	if err != nil {
		return te, err
	}
	te.LeecherCount = int(leechers)

	// loop until next b tag
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.StartTagToken && t.Data == "b" {
			break
		}
	}

	// Uploader
	z.Next() // text in font
	t = z.Token()
	te.Uploader = t.Data

	return te, nil
}

func Details(c *Connection, id int64, files bool, peers bool, snatches bool) (TorrentEntry, error) {
	if err := c.assureLogin(); err != nil {
		return TorrentEntry{}, err
	}
	data := url.Values{"id": {fmt.Sprintf("%d", id)}}
	if files {
		data.Set("filelist", "1")
	}
	if peers {
		data.Set("dllist", "1")
	}
	resp, err := c.get(c.buildUrl("/details.php", data))
	if err != nil {
		return TorrentEntry{}, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	debugRequest(resp, string(body))

	if resp.StatusCode == 404 {
		return TorrentEntry{}, errors.New("torrent not found")
	}

	te, err := parseTorrentDetails(bytes.NewReader(body), files, peers)
	if err != nil {
		return TorrentEntry{}, err
	}

	if snatches {
		data := url.Values{"id": {fmt.Sprintf("%d", id)}}
		resp, err := c.get(c.buildUrl("/viewsnatches.php", data))
		if err != nil {
			return TorrentEntry{}, err
		}
		defer resp.Body.Close()
		body, err := ioutil.ReadAll(resp.Body)
		debugRequest(resp, string(body))

		if resp.StatusCode == 404 {
			return te, nil
		}

		reader := bytes.NewReader(body)
		snatches := make([]Snatch, 0)
		foundSnatches := make(map[string]Snatch)
		maxpage := int64(0)
		chSnatch := make(chan Snatch)
		chFinished := make(chan bool)

		go func(reader io.Reader, chSnatch chan Snatch, chFinished chan bool) {
			defer func() {
				// Notify that we're done after this function
				chFinished <- true
			}()
			parseSnatches(reader, chSnatch)
		}(reader, chSnatch, chFinished)

		re, _ := regexp.Compile("<a href=\"(.+&page=(\\d+))\".*>")
		if re.MatchString(string(body)) {
			matches := re.FindAllStringSubmatch(string(body), -1)
			for _, m := range matches {
				page, _ := strconv.ParseInt(m[2], 10, 32)
				if page > maxpage {
					maxpage = page
				}
			}

			//fmt.Println("Pages: ", maxpage)

			for p := int64(1); p <= maxpage; p++ {
				data.Set("page", fmt.Sprintf("%d", p))
				pageUrl := c.buildUrl("/viewsnatches.php", data)
				go crawlSnatchList(c, pageUrl, p, chSnatch, chFinished)
			}
		}

		for p := int64(0); p <= maxpage; {
			select {
			case snatch := <-chSnatch:
				foundSnatches[snatch.Name] = snatch
				//fmt.Println("found torrent:", torrent.Id)
			case <-chFinished:
				p++
				//fmt.Println("finished a parser. now at", p, "of", maxpage)
			}
		}

		close(chFinished)
		close(chSnatch)

		for _, snatch := range foundSnatches {
			snatches = append(snatches, snatch)
		}

		te.Snatches = snatches
	}

	return te, nil
}

func parseTorrentDetails(reader io.Reader, files, peers bool) (TorrentEntry, error) {
	z := html.NewTokenizer(reader)
	te := TorrentEntry{}

	inDetailsTable := false
	lookForTable := false

Loop:
	for {
		tt := z.Next()

		switch {
		case tt == html.ErrorToken:
			// End of the document, we're done
			break Loop
		case tt == html.StartTagToken:
			t := z.Token()

			if !lookForTable && !inDetailsTable {
				continue
			} else if lookForTable {
				if t.Data == "table" {
					inDetailsTable = true
					lookForTable = false
					break Loop
				}
			}
		case tt == html.TextToken:
			t := z.Token()

			if !lookForTable && !inDetailsTable {
				if strings.HasPrefix(t.Data, "Details zu") {
					te.Name = t.Data[11:]
					lookForTable = true
				}
			}
		}
	}

	if !inDetailsTable {
		return te, errors.New("could not find details table")
	}

	var t html.Token

	// loop until next anchor tag
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.StartTagToken && t.Data == "a" {
			break
		} else if tt == html.ErrorToken {
			return te, errors.New("ran into an infinite loop while searching")
		}
	}

	// ID
	ok, href := getAttr(t, "href")
	if !ok {
		return te, errors.New("name is missing href attr")
	}
	ire, _ := regexp.Compile("download\\.php\\?torrent=(\\d+)")
	if ire.MatchString(href) {
		id, err := strconv.ParseInt(ire.FindStringSubmatch(href)[1], 10, 32)
		if err != nil {
			return te, err
		}
		te.Id = int(id)
	}

	// loop until td tag after next td
	for i := 0; i < 2; i++ {
		for {
			tt := z.Next()
			t = z.Token()
			if tt == html.StartTagToken && t.Data == "td" {
				break
			} else if tt == html.ErrorToken {
				return te, errors.New("ran into an infinite loop while searching")
			}
		}
	}

	// Info Hash
	z.Next()
	t = z.Token()
	te.InfoHash = t.Data

	// loop until td tag after next td
	for i := 0; i < 2; i++ {
		for {
			tt := z.Next()
			t = z.Token()
			if tt == html.StartTagToken && t.Data == "td" {
				break
			} else if tt == html.ErrorToken {
				return te, errors.New("ran into an infinite loop while searching")
			}
		}
	}

	// Description
	description := ""
	// loop until next text
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.TextToken {
			description += t.Data
		} else if tt == html.EndTagToken && t.Data == "td" {
			break
		}
	}
	te.Description = description

	// Category
	// loop until text == 'Typ'
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.TextToken && t.Data == "Typ" {
			z.Next()
			z.Next()
			z.Next()
			t = z.Token()
			break
		} else if tt == html.ErrorToken {
			return te, errors.New("ran into an infinite loop while searching")
		}
	}
	cid, err := Category.ToInt(t.Data)
	if err != nil {
		cid = 0
	}
	te.Category = cid

	// Size
	// loop until text == 'Größe'
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.TextToken && strings.HasPrefix(t.Data, "Gr") && !utf8.ValidString(t.Data) { //t.Data == "Größe" {
			z.Next()
			z.Next()
			z.Next()
			t = z.Token()
			break
		} else if tt == html.ErrorToken {
			return te, errors.New("ran into an infinite loop while searching")
		}
	}
	temp := strings.Split(t.Data, " ")
	size, err := strconv.ParseUint(strings.Replace(strings.Replace(temp[2], "(", "", 1), ",", "", -1), 10, 64)
	if err != nil {
		size = 0
	}
	te.Size = size

	// Added
	// loop until text == 'Hinzugefügt'
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.TextToken && strings.HasPrefix(t.Data, "Hinzugef") && !utf8.ValidString(t.Data) { //t.Data == "Hinzugefügt" {
			z.Next()
			z.Next()
			z.Next()
			t = z.Token()
			break
		} else if tt == html.ErrorToken {
			return te, errors.New("ran into an infinite loop while searching")
		}
	}
	date, err := time.Parse("2006-01-02 15:04:05", t.Data)
	if err != nil {
		date = time.Unix(0, 0)
	}
	te.Added = date

	// loop until text == 'Fertiggestellt'
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.TextToken && t.Data == "Fertiggestellt" {
			for {
				tt := z.Next()
				t = z.Token()
				if tt == html.StartTagToken && t.Data == "td" {
					z.Next()
					t = z.Token()
					break
				} else if tt == html.ErrorToken {
					return te, errors.New("ran into an infinite loop while searching")
				}
			}
			break
		}
	}
	prs, _ := regexp.Compile("(\\d+) mal")
	if prs.MatchString(t.Data) {
		m := prs.FindStringSubmatch(t.Data)
		temp, err := strconv.ParseInt(m[1], 10, 32)
		if err != nil {
			temp = 0
		}
		te.SnatchCount = int(temp)
	}

	// Num Files
	// loop until text == 'Anzahl Dateien'
	for {
		tt := z.Next()
		t = z.Token()
		if tt == html.TextToken && t.Data == "Anzahl Dateien" {
			z.Next()
			z.Next()
			z.Next()
			z.Next()
			z.Next()
			z.Next()
			z.Next()
			t = z.Token()
			break
		} else if tt == html.ErrorToken {
			return te, errors.New("ran into an infinite loop while searching")
		}
	}
	temp = strings.Split(t.Data, " ")
	nfiles, err := strconv.ParseInt(strings.Replace(temp[0], ",", "", -1), 10, 32)
	if err != nil {
		nfiles = 0
	}
	te.FileCount = int(nfiles)
	if files {
		// loop until text == 'Dateiliste'
		for {
			tt := z.Next()
			t = z.Token()
			if tt == html.TextToken && t.Data == "Dateiliste" {
				for {
					tt := z.Next()
					t = z.Token()
					if tt == html.StartTagToken && t.Data == "table" {
						break
					} else if tt == html.ErrorToken {
						return te, errors.New("ran into an infinite loop while searching")
					}
				}
				break
			}
		}

		files, err := parseFileList(z)
		if err == nil {
			te.Files = files
		}
		te.FileCount = len(files)
	}

	// Num Peers
	if peers {
		// loop until text == 'Seeder'
		parseSeeders := true
		for {
			tt := z.Next()
			t = z.Token()
			if tt == html.TextToken && t.Data == "Seeder" {
				for {
					tt := z.Next()
					t = z.Token()
					if tt == html.StartTagToken && t.Data == "table" {
						break
					} else if tt == html.ErrorToken {
						return te, errors.New("ran into an infinite loop while searching")
					} else if tt == html.TextToken && t.Data == "0 Seeder" {
						parseSeeders = false
					}
				}
				break
			}
		}
		var seeder []Peer
		if parseSeeders {
			seeder, _ = parsePeerList(z)
			te.SeederCount = len(seeder)
		}

		// loop until text == 'Leecher'
		parseLeechers := true
		for {
			tt := z.Next()
			t = z.Token()
			if tt == html.TextToken && t.Data == "Leecher" {
				for {
					tt := z.Next()
					t = z.Token()
					if tt == html.StartTagToken && t.Data == "table" {
						break
					} else if tt == html.ErrorToken {
						return te, errors.New("ran into an infinite loop while searching")
					} else if tt == html.TextToken && t.Data == "0 Leecher" {
						parseLeechers = false
					}
				}
				break
			}
		}
		var leecher []Peer
		if parseLeechers {
			leecher, _ = parsePeerList(z)
			te.LeecherCount = len(leecher)
		}

		if parseSeeders && parseLeechers {
			te.Peers = append(seeder, leecher...)
		} else if parseSeeders {
			te.Peers = seeder
		} else if parseLeechers {
			te.Peers = leecher
		}
	} else {
		// loop until text == 'Peers'
		for {
			tt := z.Next()
			t = z.Token()
			if tt == html.TextToken && t.Data == "Peers" {
				for {
					tt := z.Next()
					t = z.Token()
					if tt == html.StartTagToken && t.Data == "td" {
						z.Next()
						t = z.Token()
						break
					} else if tt == html.ErrorToken {
						return te, errors.New("ran into an infinite loop while searching")
					}
				}
				break
			}
		}
		prs, _ := regexp.Compile("(\\d+) Seeder, (\\d+) Leecher = (\\d+) Peer\\(s\\) gesamt")
		if prs.MatchString(t.Data) {
			m := prs.FindStringSubmatch(t.Data)
			temp, err := strconv.ParseInt(m[1], 10, 32)
			if err != nil {
				temp = 0
			}
			te.SeederCount = int(temp)
			temp, err = strconv.ParseInt(m[2], 10, 32)
			if err != nil {
				temp = 0
			}
			te.LeecherCount = int(temp)
		}
	}

	return te, nil
}

func parsePeerList(z *html.Tokenizer) ([]Peer, error) {
	list := make([]Peer, 0)
	tdCounter := 0
	skipTr := true
	peer := Peer{
		Name:        "",
		Connectable: false,
		Seeder:      false,
		Uploaded:    0,
		Ulrate:      0,
		Downloaded:  0,
		Dlrate:      0,
		Ratio:       0.0,
		Completed:   0.0,
		Connected:   0,
		Client:      "",
	}

Loop:
	for {
		tt := z.Next()

		switch {
		case tt == html.StartTagToken:
			t := z.Token()
			if t.Data == "tr" {
				tdCounter = 0
			} else if !skipTr && t.Data == "td" {
				tdCounter++
			}
			if tdCounter == 8 && t.Data == "div" {
				ok, val := getAttr(t, "title")
				if ok {
					val = strings.Replace(val, "%", "", 1)
					temp, err := strconv.ParseFloat(val, 64)
					if err != nil {
						temp = 0.0
					}
					peer.Completed = temp
					if int(peer.Completed) == 100 {
						peer.Seeder = true
					}
				}
			}
		case tt == html.TextToken:
			t := z.Token()
			if t.Data == "\n" {
				continue
			}
			switch tdCounter {
			case 1:
				peer.Name = t.Data
			case 2:
				if t.Data == "Ja" {
					peer.Connectable = true
				}
			case 3:
				peer.Uploaded = stringToDatasize(t.Data)
			case 4:
				peer.Ulrate = stringToDatasize(strings.Replace(t.Data, "/s", "", 1))
			case 5:
				peer.Downloaded = stringToDatasize(t.Data)
			case 6:
				peer.Dlrate = stringToDatasize(strings.Replace(t.Data, "/s", "", 1))
			case 7:
				if t.Data == "Inf." {
					peer.Ratio = -1.0
				} else if t.Data == "---" {
					peer.Ratio = 0.0
				} else {
					temp, err := strconv.ParseFloat(t.Data, 64)
					if err != nil {
						temp = 0.0
					}
					peer.Ratio = temp
				}
			case 9:
				re, _ := regexp.Compile("(:?(\\d+)d )?([0-9:]+)")
				connected := uint64(0)
				if re.MatchString(t.Data) {
					m := re.FindStringSubmatch(t.Data)
					if m[1] != "" {
						temp, err := strconv.ParseUint(m[1], 10, 32)
						if err != nil {
							temp = 0
						}
						connected += temp * 86400
					}
					if m[2] != "" {
						temp := strings.Split(m[2], ":")
						multi := uint64(1)
						for i := len(temp) - 1; i >= 0; i-- {
							temp2, err := strconv.ParseUint(temp[i], 10, 32)
							if err != nil {
								temp2 = 0
							}
							connected += temp2 * multi
							multi *= 60
						}
					}
				}

				peer.Connected = connected
			case 11:
				peer.Client = t.Data
			}
		case tt == html.EndTagToken:
			t := z.Token()
			if t.Data == "table" {
				break Loop
			} else if skipTr && t.Data == "tr" {
				skipTr = false
			} else if t.Data == "tr" {
				list = append(list, peer)
				peer = Peer{
					Name:        "",
					Connectable: false,
					Seeder:      false,
					Uploaded:    0,
					Ulrate:      0,
					Downloaded:  0,
					Dlrate:      0,
					Ratio:       0.0,
					Completed:   0.0,
					Connected:   0,
					Client:      "",
				}
				tdCounter = -1
			}
		}
	}

	return list, nil
}

func parseFileList(z *html.Tokenizer) ([]TorrentFile, error) {
	list := make([]TorrentFile, 0)
	tdCounter := 0
	skipTr := true
	file := TorrentFile{
		Name: "",
		Size: 0,
	}

Loop:
	for {
		tt := z.Next()

		switch {
		case tt == html.StartTagToken:
			t := z.Token()
			if t.Data == "tr" {
				tdCounter = 0
			} else if !skipTr && t.Data == "td" {
				tdCounter++
			}
		case tt == html.TextToken:
			t := z.Token()
			if t.Data == "\n" {
				continue
			}
			switch tdCounter {
			case 1:
				file.Name = t.Data
			case 2:
				file.Size = stringToDatasize(t.Data)
		}
		case tt == html.EndTagToken:
			t := z.Token()
			if t.Data == "table" {
				break Loop
			} else if skipTr && t.Data == "tr" {
				skipTr = false
			} else if t.Data == "tr" {
				list = append(list, file)
				file = TorrentFile{
					Name: "",
					Size: 0,
				}
				tdCounter = -1
			}
		}
	}

	return list, nil
}

func crawlSnatchList(c *Connection, url string, page int64, chSnatch chan Snatch, chFinished chan bool) {
	resp, err := c.get(url)
	//fmt.Println("Crawl Page:", page)

	defer func() {
		// Notify that we're done after this function
		chFinished <- true
	}()

	if err != nil {
		fmt.Println("ERROR: Failed to crawl \"" + url + "\"")
		return
	}

	b := resp.Body
	defer b.Close() // close Body when the function returns

	parseSnatches(b, chSnatch)
}

func parseSnatches(reader io.Reader, ch chan Snatch) () {
	z := html.NewTokenizer(reader)

	tdCounter := 0
	skipTr := true
	snatch := Snatch{
		Name: "",
		Completed: time.Unix(0, 0),
		Ratio: 0.0,
		Downloaded: 0,
		Uploaded: 0,
		Stopped: time.Unix(0, 0),
		Seeding: false,
	}

	isInSnatchesTable := false
	lookForTable := false

Loop:
	for {
		tt := z.Next()

		switch {
		case tt == html.ErrorToken:
			// End of the document, we're done
			break Loop
		case tt == html.StartTagToken:
			t := z.Token()

			if !lookForTable && !isInSnatchesTable {
				continue
			} else if lookForTable {
				if t.Data == "table" {
					isInSnatchesTable = true
					lookForTable = false
					break Loop
				}
			}
		case tt == html.TextToken:
			t := z.Token()

			if !lookForTable && !isInSnatchesTable {
				if t.Data == "Mitglieder die dieses Torrent gedownloadet haben" {
					lookForTable = true
				}
			}
		}
	}

	parseRatio := false

InnerLoop:
	for {
		tt := z.Next()

		switch {
		case tt == html.StartTagToken:
			t := z.Token()
			if t.Data == "tr" {
				tdCounter = 0
			} else if !skipTr && t.Data == "td" {
				tdCounter++
			}
		case tt == html.TextToken:
			t := z.Token()
			if t.Data == "\n" {
				continue
			}
			switch tdCounter {
			case 1:
				snatch.Name = t.Data
			case 2:
				if strings.HasPrefix(t.Data, "Torrent:") {
					snatch.Uploaded = stringToDatasize(strings.TrimPrefix(t.Data, "Torrent: "))
				}
			case 3:
				if strings.HasPrefix(t.Data, "Torrent:") {
					snatch.Downloaded = stringToDatasize(strings.TrimPrefix(t.Data, "Torrent: "))
				}
			case 4:
				if strings.HasPrefix(t.Data, "Torrent:") {
					parseRatio = true
				} else if parseRatio {
					if t.Data == "Inf." {
						snatch.Ratio = -1.0
					} else if t.Data == "---" {
						snatch.Ratio = 0.0
					} else {
						temp, err := strconv.ParseFloat(t.Data, 64)
						if err != nil {
							fmt.Println(err.Error())
							temp = 0.0
						}
						snatch.Ratio = temp
					}
					parseRatio = false
				}
			case 5:
				date, err := time.Parse("2006-01-02 15:04:05", t.Data)
				if err != nil {
					date = time.Unix(0, 0)
				}
				snatch.Completed = date
			case 6:
				if t.Data == "Seedet im Moment" {
					snatch.Seeding = true
				} else {
					date, err := time.Parse("2006-01-02 15:04:05", t.Data)
					if err != nil {
						date = time.Unix(0, 0)
					}
					snatch.Stopped = date
				}
			}
		case tt == html.EndTagToken:
			t := z.Token()
			if t.Data == "table" {
				break InnerLoop
			} else if skipTr && t.Data == "tr" {
				skipTr = false
			} else if t.Data == "tr" {
				ch <- snatch
				snatch = Snatch{
					Name: "",
					Completed: time.Unix(0, 0),
					Ratio: 0.0,
					Downloaded: 0,
					Uploaded: 0,
					Stopped: time.Unix(0, 0),
					Seeding: false,
				}
				tdCounter = -1
			}
		}
	}

	if !isInSnatchesTable {
		return
	}
}

func Thank(c *Connection, id int64) (bool, error) {
	c.assureLogin()

	resp, err := c.get(c.buildUrl("thanksajax.php", url.Values{"torrentid": {fmt.Sprintf("%d", id)}}))
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	debugRequest(resp, string(body))

	if resp.StatusCode == 404 {
		return false, errors.New("torrent not found")
	}

	if strings.Contains(string(body), "<span>Fehler</span>") {
		return false, errors.New("account parked")
	}
	if strings.Contains(string(body), "<span>ERROR</span>") {
		return false, errors.New("missing torrent id")
	}

	return true, nil
}

// Helper function to pull the class attribute from a Token
func getCssClass(t html.Token) (bool, string) {
	return getAttr(t, "class")
}

// Helper function to pull an attribute from a Token
func getAttr(t html.Token, attr string) (ok bool, val string) {
	// Iterate over all of the Token's attributes until we find an "attr"
	for _, a := range t.Attr {
		if a.Key == attr {
			val = a.Val
			ok = true
			break
		}
	}

	// "bare" return will return the variables (ok, val) as defined in
	// the function definition
	return
}

func stringToDatasize(str string) (uint64) {
	temp := strings.Split(str, " ")
	if len(temp) == 1 {
		return 0
	}
	temp[0] = strings.Replace(temp[0], ".", "", -1)
	temp[0] = strings.Replace(temp[0], ",", ".", 1)
	temp2, err := strconv.ParseFloat(temp[0], 64)
	if err != nil {
		temp2 = 0.0
	}
	var temp3 uint64
	switch temp[1] {
	case "KB":
		temp3 = uint64(temp2 * float64(datasize.KB))
	case "MB":
		temp3 = uint64(temp2 * float64(datasize.MB))
	case "GB":
		temp3 = uint64(temp2 * float64(datasize.GB))
	case "TB":
		temp3 = uint64(temp2 * float64(datasize.TB))
	case "PB":
		temp3 = uint64(temp2 * float64(datasize.PB))
	case "EB":
		temp3 = uint64(temp2 * float64(datasize.EB))
	default:
		temp3 = uint64(temp2)
	}

	return temp3
}
