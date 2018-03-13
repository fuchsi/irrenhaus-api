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

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"

	"github.com/c2h5oh/datasize"
	"github.com/fuchsi/irrenhaus-api/Category"
)

const (
	pageErrorUploadFailed = "TorrentUpload-Upload fehlgeschlagen!"
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

func (t *TorrentUpload) Upload() error {
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
		debugLog("error writing to buffer")
		return err
	}
	_, err = io.Copy(metaWriter, t.Meta)
	if err != nil {
		return err
	}

	nfoWriter, err := bodyWriter.CreateFormFile("nfo", t.Name+".nfo")
	if err != nil {
		debugLog("error writing to buffer")
		return err
	}
	_, err = io.Copy(nfoWriter, t.Nfo)
	if err != nil {
		return err
	}

	image1Writer, err := bodyWriter.CreateFormFile("pic1", t.Name+".jpg")
	if err != nil {
		debugLog("error writing to buffer")
		return err
	}
	_, err = io.Copy(image1Writer, t.Image1)
	if err != nil {
		return err
	}

	if t.Image2 != nil {
		image2Writer, err := bodyWriter.CreateFormFile("pic1", t.Name+"_2"+".jpg")
		if err != nil {
			debugLog("error writing to buffer")
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

	uploadFailed := false

	doc, err := goquery.NewDocumentFromResponse(resp)
	sel := doc.Find(".centeredtitle span")
	for i := range sel.Nodes {
		node := sel.Eq(i)
		if node.Text() == pageErrorUploadFailed {
			uploadFailed = true
			break
		}
	}

	if uploadFailed {
		var errorMsg string

		sel = doc.Find("p+p[style=color:red]")
		if len(sel.Nodes) > 0 {
			node := sel.Eq(0)
			errorMsg = node.Text()
		}

		if errorMsg == "" {
			errorMsg = "unknown error"
		}

		return errors.New("upload failed: " + errorMsg)
	}

	sel = doc.Find("a[href^=details.php]")
	if len(sel.Nodes) > 0 {
		link := sel.Eq(0)
		href, _ := link.Attr("href")
		re, _ := regexp.Compile("details\\.php\\?id=(\\d+)")
		if re.MatchString(href) {
			t.Id, err = strconv.ParseInt(re.FindStringSubmatch(sbody)[1], 10, 64)
			if err != nil {
				return err
			}
		}
	}

	return errors.New("upload failed")
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

	doc, err := goquery.NewDocumentFromResponse(resp)
	if err != nil {
		return nil, err
	}

	re, _ := regexp.Compile("page=(\\d+)")
	sel := doc.Find("p[align=center] a")
	for i := range sel.Nodes {
		node := sel.Eq(i)
		href, _ := node.Attr("href")
		matches := re.FindAllStringSubmatch(href, -1)
		for _, m := range matches {
			page, _ := strconv.ParseInt(m[1], 10, 32)
			if page > maxpage {
				maxpage = page
			}
		}
	}

	if maxpage > 0 {
		for p := int64(1); p <= maxpage; p++ {
			data.Set("page", fmt.Sprintf("%d", p))
			pageURL := c.buildUrl("/browse.php", data)
			go crawlTorrentList(c, pageURL, p, chTorrents, chFinished)
		}
	}

	for p := int64(0); p <= maxpage; {
		select {
		case torrent := <-chTorrents:
			foundTorrents[torrent.Id] = torrent
			//debugLog("found torrent:", torrent.Id)
		case <-chFinished:
			p++
			//debugLog("finished a parser. now at", p, "of", maxpage)
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
	//debugLog("Crawl Page:", page)

	defer func() {
		// Notify that we're done after this function
		chFinished <- true
	}()

	if err != nil {
		debugLog("ERROR: Failed to crawl \"" + url + "\"")
		return
	}

	b := resp.Body
	defer b.Close() // close Body when the function returns

	parseTorrentList(b, chTorrents)
}

func parseTorrentList(body io.Reader, ch chan TorrentEntry) {
	debugLog("Parsing Torrent List")

	doc, err := goquery.NewDocumentFromReader(body)
	if err != nil {
		return
	}
	doc.Find("table.tableinborder").Each(func(i int, s *goquery.Selection) {
		firstTd := s.Find("td").First()
		if firstTd.Text() != "Typ" {
			return
		}
		s.Find("tr").Each(func(i int, s *goquery.Selection) {
			if i == 0 {
				return
			}
			torrentEntry, err := parseTorrentEntry(s)
			if err != nil {
				debugLog("ERROR while parsing the torrent entry:", err.Error())
				return
			}
			//debugLog(torrentEntry)
			ch <- torrentEntry
		})
	})
}

func parseTorrentEntry(s *goquery.Selection) (TorrentEntry, error) {
	te := TorrentEntry{}
	debugLog("Parsing Torrent Entry")

	tds := s.Find("td")

	// Category
	href, ok := tds.Eq(0).Find("a").First().Attr("href")
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

	// ID
	link := tds.Eq(1).Find("a").First()
	href, ok = link.Attr("href")
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
	name, ok := link.Attr("title")
	if !ok {
		name = link.Text()
	}
	te.Name = name

	// Files

	files, err := strconv.ParseInt(tds.Eq(2).Find("a").First().Text(), 10, 32)
	if err != nil {
		return te, err
	}
	te.FileCount = int(files)

	// Comments
	comments, err := strconv.ParseInt(tds.Eq(3).Find("a").First().Text(), 10, 32)
	if err != nil {
		return te, err
	}
	te.CommentCount = int(comments)

	// Added date/time
	addedTimestamp := tds.Eq(4).Text()
	te.Added, err = time.Parse("02.01.200615:04:05", addedTimestamp)
	if err != nil {
		return te, err
	}

	// Size
	rawSize := tds.Eq(6).Text()
	commaIndex := strings.IndexByte(rawSize, ',')
	// get the part before the ','
	size, err := strconv.ParseInt(rawSize[0:commaIndex], 10, 32)
	if err != nil {
		return te, err
	}
	// part after the ','
	size2, err := strconv.ParseInt(rawSize[(commaIndex+1):(commaIndex+3)], 10, 32)
	if err != nil {
		return te, err
	}
	// combine both
	size *= 100
	size += size2
	realsize := float64(size) / 100

	switch rawSize[(commaIndex + 3):] {
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

	// Snatch Count
	snatches, err := strconv.ParseInt(tds.Eq(8).Find("a").First().Text(), 10, 32)
	if err != nil {
		return te, err
	}
	te.SnatchCount = int(snatches)

	// Seeder Count
	seeders, err := strconv.ParseInt(tds.Eq(9).Find("a").First().Text(), 10, 32)
	if err != nil {
		return te, err
	}
	te.SeederCount = int(seeders)

	// Leecher Count
	leechers, err := strconv.ParseInt(tds.Eq(10).Find("a").First().Text(), 10, 32)
	if err != nil {
		return te, err
	}
	te.LeecherCount = int(leechers)

	// Uploader
	link = tds.Eq(12).Find("a")
	if len(link.Nodes) == 1 {
		te.Uploader = link.Text()
	} else {
		te.Uploader = "anon"
	}

	return te, nil
}

func Details(c *Connection, id int64, files bool, peers bool, snatches bool) (*TorrentEntry, error) {
	if err := c.assureLogin(); err != nil {
		return nil, err
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
		return nil, err
	}
	defer resp.Body.Close()
	// encode the response from iso-8859-1, or the umlauts are fucked
	rd := transform.NewReader(resp.Body, charmap.ISO8859_1.NewDecoder())
	body, err := ioutil.ReadAll(rd)
	debugRequest(resp, string(body))

	if resp.StatusCode == 404 {
		return nil, errors.New("torrent not found")
	}

	te, err := parseTorrentDetails(bytes.NewReader(body), files, peers)
	if err != nil {
		return nil, err
	}

	if snatches {
		data := url.Values{"id": {fmt.Sprintf("%d", id)}}
		resp, err := c.get(c.buildUrl("/viewsnatches.php", data))
		if err != nil {
			return nil, err
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

			//debugLog("Pages: ", maxpage)

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
				//debugLog("found torrent:", torrent.Id)
			case <-chFinished:
				p++
				//debugLog("finished a parser. now at", p, "of", maxpage)
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

func parseTorrentDetails(reader io.Reader, files, peers bool) (*TorrentEntry, error) {
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return nil, err
	}

	te := TorrentEntry{}
	var detailsTable *goquery.Selection
	divs := doc.Find("div.blockinborder")
	for i := range divs.Nodes {
		node := divs.Eq(i)
		if !strings.HasPrefix(node.Find("div.centeredtitle b").Text(), "Details zu") {
			continue
		}

		te.Name = strings.TrimPrefix(node.Find("div.centeredtitle b").Text(), "Details zu ")
		detailsTable = node.Find("div>table.tableinborder")
		break
	}

	if detailsTable == nil {
		return nil, errors.New("could not find details table")
	}

	trs := detailsTable.Find("tbody:first-child>tr")
	row := 0

	// ID
	href, ok := trs.Eq(row).Find("td a").Attr("href")
	if !ok {
		return &te, errors.New("name is missing href attr")
	}

	ire, _ := regexp.Compile("download\\.php\\?torrent=(\\d+)")
	if ire.MatchString(href) {
		id, err := strconv.ParseInt(ire.FindStringSubmatch(href)[1], 10, 32)
		if err != nil {
			return &te, err
		}
		te.Id = int(id)
	}

	// Info Hash
	row++
	te.InfoHash = trs.Eq(row).Find("td").Eq(1).Text()

	// Description
	description := ""
	row++
	rawDescription, err := trs.Eq(row).Find("td").Eq(1).After("center").Html()
	if err == nil {
		// strip all html tags, i think we can use the shoutbox function for this task

		description = ShoutboxStrip(rawDescription, "")
	}
	te.Description = description

	// Category
	row += 2
	cid, err := Category.ToInt(getSecondTd(trs, row).Text())
	if err != nil {
		cid = 0
	}
	te.Category = cid

	// Size
	// Looks like 117,73 GB (123,456,789 Bytes)
	row += 2
	temp := strings.Split(getSecondTd(trs, row).Text(), " ")
	// convert '(123,456,789' to a uint
	size, err := strconv.ParseUint(strings.Replace(strings.Replace(temp[2], "(", "", 1), ",", "", -1), 10, 64)
	if err != nil {
		size = 0
	}
	te.Size = size

	// Added
	row++
	date, err := time.Parse("2006-01-02 15:04:05", getSecondTd(trs, row).Text())
	if err != nil {
		date = time.Unix(0, 0)
	}
	te.Added = date

	// loop until text == 'Fertiggestellt'
	prs, _ := regexp.Compile("(\\d+) mal")
	row += 6
	if prs.MatchString(getSecondTd(trs, row).Text()) {
		m := prs.FindStringSubmatch(getSecondTd(trs, row).Text())
		temp, err := strconv.ParseInt(m[1], 10, 32)
		if err != nil {
			temp = 0
		}
		te.SnatchCount = int(temp)
	}

	// Num Files
	row += 2
	temp = strings.Split(getSecondTd(trs, row).Text(), " ")
	nfiles, err := strconv.ParseInt(strings.Replace(temp[0], ",", "", -1), 10, 32)
	if err != nil {
		nfiles = 0
	}
	te.FileCount = int(nfiles)
	if files {
		row++
		files, err := parseFileList(getSecondTd(trs, row).Find("table"))
		if err == nil {
			te.Files = files
		}
		te.FileCount = len(files)
		// amount of <tr> elements from file table to row count
		row += len(files) + 1
	}

	// Num Peers
	if peers {
		row += 2
		sTable := getSecondTd(trs, row).Find("table")
		parseSeeders := len(sTable.Nodes) > 0
		var seeder []Peer
		if parseSeeders {
			seeder, _ = parsePeerList(sTable)
			te.SeederCount = len(seeder)
			// add amount of <tr> elements from seeders table to row count
			row += te.SeederCount + 1
		}

		row++
		pTable := getSecondTd(trs, row).Find("table")
		parseLeechers := len(pTable.Nodes) > 0

		var leecher []Peer
		if parseLeechers {
			leecher, _ = parsePeerList(pTable)
			te.LeecherCount = len(leecher)
			// add amount of <tr> elements from leechers table to row count
			row += te.LeecherCount + 1
		}

		if parseSeeders && parseLeechers {
			te.Peers = append(seeder, leecher...)
		} else if parseSeeders {
			te.Peers = seeder
		} else if parseLeechers {
			te.Peers = leecher
		}
	} else {
		row += 2

		prs, _ := regexp.Compile("(\\d+) Seeder, (\\d+) Leecher = (\\d+) Peer\\(s\\) gesamt")
		if prs.MatchString(getSecondTd(trs, row).Text()) {
			m := prs.FindStringSubmatch(getSecondTd(trs, row).Text())
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

	return &te, nil
}

func parsePeerList(s *goquery.Selection) ([]Peer, error) {
	list := make([]Peer, 0)
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

	re, _ := regexp.Compile("(:?(\\d+)d )?([0-9:]+)")

	s.Find("tr").Each(func(i int, s *goquery.Selection) {
		if i == 0 {
			return
		}

		tds := s.Find("td")
		col := 0
		td := tds.Eq(col)

		if len(td.Find("a").Nodes) > 0 {
			peer.Name = td.Find("a").Text()
		} else {
			peer.Name = td.Text()
		}

		col++
		td = tds.Eq(col)
		peer.Connectable = td.Text() == "Ja"

		col++
		td = tds.Eq(col)
		peer.Uploaded = stringToDatasize(td.Text())

		col++
		td = tds.Eq(col)
		peer.Ulrate = stringToDatasize(strings.TrimSuffix(td.Text(), "/s"))

		col++
		td = tds.Eq(col)
		peer.Downloaded = stringToDatasize(td.Text())

		col++
		td = tds.Eq(col)
		peer.Dlrate = stringToDatasize(strings.TrimSuffix(td.Text(), "/s"))

		col++
		td = tds.Eq(col)
		if td.Text() == "Inf." {
			peer.Ratio = -1.0
		} else if td.Text() == "---" {
			peer.Ratio = 0.0
		} else {
			temp, err := strconv.ParseFloat(td.Find("font").Text(), 64)
			if err != nil {
				temp = 0.0
			}
			peer.Ratio = temp
		}

		col++
		div := tds.Eq(col).Find("div")
		val, ok := div.Attr("title")
		val = strings.Replace(val, "%", "", 1)
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

		col++
		td = tds.Eq(col)
		connected := uint64(0)
		if re.MatchString(td.Text()) {
			m := re.FindStringSubmatch(td.Text())
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

		col += 2
		td = tds.Eq(col)
		peer.Client = td.Text()

		// append peer to list
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
	})

	return list, nil
}

func parseFileList(s *goquery.Selection) ([]TorrentFile, error) {
	list := make([]TorrentFile, 0)

	s.Find("tr").Each(func(i int, s *goquery.Selection) {
		if i == 0 {
			return
		}

		tds := s.Find("td")

		file := TorrentFile{
			Name: tds.Eq(0).Text(),
			Size: stringToDatasize(tds.Eq(1).Text()),
		}

		list = append(list, file)
	})

	return list, nil
}

func crawlSnatchList(c *Connection, url string, page int64, chSnatch chan Snatch, chFinished chan bool) {
	resp, err := c.get(url)
	//debugLog("Crawl Page:", page)

	defer func() {
		// Notify that we're done after this function
		chFinished <- true
	}()

	if err != nil {
		debugLog("ERROR: Failed to crawl \"" + url + "\"")
		return
	}

	b := resp.Body
	defer b.Close() // close Body when the function returns

	parseSnatches(b, chSnatch)
}

func parseSnatches(reader io.Reader, ch chan Snatch) {
	doc, err := goquery.NewDocumentFromReader(reader)
	if err != nil {
		return
	}

	t := doc.Find("table.tableb")
	if len(t.Nodes) == 0 {
		return
	}
	table := t.Eq(0)

	table.Find("tr").Each(func(i int, s *goquery.Selection) {
		if i == 0 {
			return
		}

		snatch := Snatch{
			Name:       "",
			Completed:  time.Unix(0, 0),
			Ratio:      0.0,
			Downloaded: 0,
			Uploaded:   0,
			Stopped:    time.Unix(0, 0),
			Seeding:    false,
		}

		col := 0
		td := s.Find("td").Eq(col)
		snatch.Name = td.Find("a").Text()

		col++
		td = s.Find("td").Eq(col)
		t := td.Find("b").Text()

		snatch.Downloaded = stringToDatasize(strings.TrimPrefix(t, "Torrent: "))

		col++
		td = s.Find("td").Eq(col)
		t = td.Find("b").Text()

		snatch.Uploaded = stringToDatasize(strings.TrimPrefix(t, "Torrent: "))

		col++
		td = s.Find("td").Eq(col)
		t = td.Find("b").Text()
		t = strings.TrimPrefix(t, "Torrent: ")

		if t == "Inf." {
			snatch.Ratio = -1.0
		} else if t == "---" {
			snatch.Ratio = 0.0
		} else {
			temp, err := strconv.ParseFloat(t, 64)
			if err != nil {
				debugLog(err.Error())
				temp = 0.0
			}
			snatch.Ratio = temp
		}

		col++
		td = s.Find("td").Eq(col)
		t = td.Find("b").Text()

		date, err := time.Parse("2006-01-02 15:04:05", t)
		if err != nil {
			date = time.Unix(0, 0)
		}
		snatch.Completed = date

		col++
		td = s.Find("td").Eq(col)
		t = td.Find("font").Text()

		if t == "Seedet im Moment" {
			snatch.Seeding = true
		} else {
			date, err := time.Parse("2006-01-02 15:04:05", t)
			if err != nil {
				date = time.Unix(0, 0)
			}
			snatch.Stopped = date
		}

		ch <- snatch
	})
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

func stringToDatasize(str string) uint64 {
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

func getSecondTd(s *goquery.Selection, nthTr int) *goquery.Selection {
	return s.Eq(nthTr).Find("td").Eq(1)
}
