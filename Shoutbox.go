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
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
)

type ShoutboxMessage struct {
	Id      int64
	User    string
	UserId  int
	Date    time.Time
	Message string
}

var shoutboxRegexp map[string]*regexp.Regexp

func ShoutboxRead(c *Connection, shoutId int, lastMessageId int64) ([]ShoutboxMessage, error) {
	c.assureLogin()

	data := url.Values{}
	data.Add("b", fmt.Sprintf("%d", shoutId))
	if lastMessageId > 0 {
		data.Add("lid", fmt.Sprintf("%d", lastMessageId))
	}

	resp, err := c.get(c.buildUrl("shoutx.php", data))
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	rd := transform.NewReader(resp.Body, charmap.ISO8859_1.NewDecoder())
	body, err := ioutil.ReadAll(rd)
	debugRequest(resp, string(body))
	if len(body) <= 1 {
		return nil, nil // no error, just no new data
	}

	messages := make([]ShoutboxMessage, 0)
	jsonMsg := make([][]string, 0)
	err = json.Unmarshal(body, &jsonMsg)
	if err != nil {
		return nil, err
	}

	for _, jmsg := range jsonMsg {
		if jmsg[0] == "" {
			continue
		}
		id, err := strconv.ParseInt(jmsg[0], 10, 32)
		if err != nil {
			debugLog("[ShoutboxRead]", err.Error())
		}
		uid, err := strconv.ParseInt(jmsg[1], 10, 32)
		if err != nil {
			debugLog("[ShoutboxRead]", err.Error())
		}
		date, err := time.Parse("02.01. 15:04", jmsg[2])
		if err != nil {
			debugLog("[ShoutboxRead]", err.Error())
		}
		strMsg := ShoutboxStrip(jmsg[5])
		msg := ShoutboxMessage{
			Id:      id,
			UserId:  int(uid),
			User:    jmsg[4],
			Date:    date,
			Message: strMsg,
		}

		messages = append(messages, msg)
	}

	// reverse messages
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}

// Strip the HTML / format code from the message
func ShoutboxStrip(msg string) (stripped string) {
	if len(shoutboxRegexp) == 0 {
		shoutboxRegexpInit()
	}

	stripped = shoutboxRegexp["center"].ReplaceAllString(msg, "$1")
	stripped = shoutboxRegexp["bold"].ReplaceAllString(stripped, "$1")
	stripped = shoutboxRegexp["italic"].ReplaceAllString(stripped, "$1")
	stripped = shoutboxRegexp["underline"].ReplaceAllString(stripped, "$1")
	stripped = shoutboxRegexp["img2"].ReplaceAllString(stripped, "$2")
	stripped = shoutboxRegexp["img"].ReplaceAllString(stripped, "$1")
	stripped = shoutboxRegexp["img3"].ReplaceAllString(stripped, "$1")
	stripped = shoutboxRegexp["color"].ReplaceAllString(stripped, "$2")
	stripped = shoutboxRegexp["link"].ReplaceAllString(stripped, "$4 [$1]")
	stripped = shoutboxRegexp["link2"].ReplaceAllString(stripped, "$4 [https://irrenhaus.dyndns.dk$1]") // fix hardcoded url
	stripped = shoutboxRegexp["size"].ReplaceAllString(stripped, "$2")
	stripped = shoutboxRegexp["font"].ReplaceAllString(stripped, "$2")
	stripped = shoutboxRegexp["nfo"].ReplaceAllString(stripped, "$1")
	stripped = shoutboxRegexp["pre"].ReplaceAllString(stripped, "$1")
	stripped = shoutboxRegexp["hxxp"].ReplaceAllString(stripped, "http$1://$2")

	stripped = strings.Replace(stripped, "<br>", "\n", -1)
	stripped = strings.Replace(stripped, "&nbsp;", " ", -1)

	return
}

func ShoutboxWrite(c *Connection, shoutId int, message string) (bool, error) {
	c.assureLogin()

	data := url.Values{}
	data.Add("b", fmt.Sprintf("%d", shoutId))
	datap := url.Values{}
	datap.Add("shbox_text", message)

	resp, err := c.postForm(c.buildUrl("shoutx.php", data), datap)
	if err != nil {
		return false, err
	}

	defer resp.Body.Close()
	rd := transform.NewReader(resp.Body, charmap.ISO8859_1.NewDecoder())
	body, err := ioutil.ReadAll(rd)
	debugRequest(resp, string(body))

	jsonMsg := make([][]string, 0)
	err = json.Unmarshal(body, &jsonMsg)
	if err != nil {
		return false, err
	}

	for _, jmsg := range jsonMsg {
		if jmsg[0] == "" {
			continue
		}
		uid, err := strconv.ParseInt(jmsg[1], 10, 32)
		if err != nil {
			debugLog("[ShoutboxWrite]", err.Error())
		}
		if uid == c.cookies.Uid {
			if jmsg[5] == message { // this may fail badly if the original message contained format code
				return true, nil
			}
		}
	}

	return false, nil
}

// Initialize the shoutbox regexp objects
func shoutboxRegexpInit() {
	shoutboxRegexp = make(map[string]*regexp.Regexp)
	shoutboxRegexp["center"], _ = regexp.Compile("<center>(.+)</center>")
	shoutboxRegexp["bold"], _ = regexp.Compile("<b>(.+)</b>")
	shoutboxRegexp["italic"], _ = regexp.Compile("<i>(.+)</i>")
	shoutboxRegexp["underline"], _ = regexp.Compile("<u>(.+)</u>")
	shoutboxRegexp["img"], _ = regexp.Compile("<img src=\"([^\"]+)\" alt=\"\" border=\"0\">")
	shoutboxRegexp["img2"], _ = regexp.Compile("<img src=\"(/pic/smilies/[^\"]+)\" border=\"0\" alt=\"([^\"]+)\">")
	shoutboxRegexp["img3"], _ = regexp.Compile("<img border=\"0\" src=\"([^\"]+)\" alt=\"\">")
	shoutboxRegexp["color"], _ = regexp.Compile("<font color=\"?([a-zA-Z]+|#[0-9a-fA-F]+)\"?>(.+?)</font>")
	shoutboxRegexp["link"], _ = regexp.Compile("<a href=\"(https?://[^\"]+)\"(:? target=\"([^\"]+)\")?>(.+)</a>")
	shoutboxRegexp["link2"], _ = regexp.Compile("<a href=\"(/[^\"]+)\"(:? target=\"([^\"]+)\")?>(.+)</a>")
	shoutboxRegexp["size"], _ = regexp.Compile("<font size=\"?(\\d+)\"?>(.+)</font>")
	shoutboxRegexp["font"], _ = regexp.Compile("<font face=\"([^\"]+)\">(.+)</font>")
	shoutboxRegexp["nfo"], _ = regexp.Compile("<tt><nobr><font face=\"MS Linedraw\" size=\"2\" style=\"font-size: 10pt; line-height: 10pt\">(.+)</font></nobr></tt>")
	shoutboxRegexp["pre"], _ = regexp.Compile("<tt><nobr>(.+)</nobr></tt>")
	shoutboxRegexp["hxxp"], _ = regexp.Compile("hxxp(s)?://([^ ]+)")
}
