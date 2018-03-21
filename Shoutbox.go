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
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/ioutil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Unnamed events are still unknown
const (
	ShoutboxEventNone = 0
	ShoutboxEvent1    = 1
	// if set, unread message count is in data[1]
	ShoutboxEventUserMessage = 2
	ShoutboxEvent4           = 4
	ShoutboxEvent8           = 8
	ShoutboxEvent16          = 16
	ShoutboxEvent32          = 32
	// if set, one of the following to operations are possible
	// simple message delete: data[3] contains 'del,ID1,ID2,...' indicating which message should be deleted
	// clear entire chat: data[3] is 'clear'
	ShoutboxEventDeleteEntry = 64
)

type ShoutboxMessage struct {
	Id      int64
	User    string
	UserId  int
	Date    time.Time
	Message string

	Event *ShoutboxEvent
}

type ShoutboxEvent struct {
	Type int
	ID   int
	Data []string
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
	// sanitize the json input
	body, err := sanitizeJSON(resp.Body)
	if err != nil {
		return nil, err
	}
	debugRequest(resp, string(body))
	if len(body) <= 1 {
		return nil, nil // no error, just no new data
	}

	messages := make([]ShoutboxMessage, 0)
	jsonMsg := make([][]string, 0)
	err = json.Unmarshal(body, &jsonMsg)
	if err != nil {
		if bytes.Contains(body, []byte("Die Serverlast ist Momentan zu hoch")) {
			return nil, errors.New("serverload")
		}
		debugRequest(resp, string(body))
		return nil, err
	}

	for i, jmsg := range jsonMsg {
		// control messages
		if i == 0 {
			eventType, err := strconv.ParseInt(jmsg[0], 10, 32)
			if err != nil {
				debugLog("[ShoutboxRead]", err.Error())
			}
			eventID, err := strconv.ParseInt(jmsg[1], 10, 32)
			if err != nil {
				debugLog("[ShoutboxRead]", err.Error())
			}
			eventData := make([]string, 4)
			eventData[0] = jmsg[3]
			eventData[1] = jmsg[4]
			eventData[2] = jmsg[5]
			eventData[3] = jmsg[6]

			event := ShoutboxEvent{
				Type: int(eventType),
				ID:   int(eventID),
				Data: eventData,
			}

			message := ShoutboxMessage{Event: &event}
			messages = append(messages, message)
			continue
		}
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
		messageType := jmsg[6]
		if messageType != "" {
			debugLog("unsuppored message type:" + messageType)
			continue
		}
		strMsg := ShoutboxStrip(jmsg[5], c.url)
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
func ShoutboxStrip(msg, url string) (stripped string) {
	if len(shoutboxRegexp) == 0 {
		shoutboxRegexpInit()
	}

	stripped = shoutboxRegexp["center"].ReplaceAllString(msg, "$1")
	stripped = shoutboxRegexp["bold"].ReplaceAllString(stripped, "$1")
	stripped = shoutboxRegexp["italic"].ReplaceAllString(stripped, "$1")
	stripped = shoutboxRegexp["underline"].ReplaceAllString(stripped, "$1")
	stripped = shoutboxRegexp["emoji"].ReplaceAllString(stripped, "emoji:$2")
	stripped = shoutboxRegexp["img"].ReplaceAllString(stripped, "$1")
	stripped = shoutboxRegexp["img3"].ReplaceAllString(stripped, "$1")
	stripped = shoutboxRegexp["color"].ReplaceAllString(stripped, "$2")
	stripped = shoutboxRegexp["link"].ReplaceAllString(stripped, "$4 [$1]")
	stripped = shoutboxRegexp["link2"].ReplaceAllString(stripped, fmt.Sprintf("$4 [%s$1]", url)) // fix hardcoded url
	stripped = shoutboxRegexp["size"].ReplaceAllString(stripped, "$2")
	stripped = shoutboxRegexp["font"].ReplaceAllString(stripped, "$2")
	stripped = shoutboxRegexp["nfo"].ReplaceAllString(stripped, "$1")
	stripped = shoutboxRegexp["pre"].ReplaceAllString(stripped, "$1")
	stripped = shoutboxRegexp["hxxp"].ReplaceAllString(stripped, "http$1://$2")

	stripped = strings.Replace(stripped, "<br>\n", "\n", -1)
	stripped = strings.Replace(stripped, "<br>", "", -1)
	stripped = strings.Replace(stripped, "<br/>\n", "\n", -1)
	stripped = strings.Replace(stripped, "<br/>", "", -1)
	stripped = strings.Replace(stripped, "&nbsp;", " ", -1)

	stripped = emojify(stripped)

	stripped = html.UnescapeString(stripped)

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
	// sanitize the json input
	body, err := sanitizeJSON(resp.Body)
	if err != nil {
		return false, err
	}
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
	shoutboxRegexp["emoji"], _ = regexp.Compile("<img.*?src=\"(/pic/smilies/([^\"]+))\"[^>]*>")
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

var emojis map[string]rune

func emojiInit() {
	if len(emojis) > 0 {
		return
	}

	emojis = make(map[string]rune)

	emojis["smile1.gif"] = 0x1F600
	emojis["zwinkern.gif"] = 0x1F609
	emojis["bf.gif"] = 0x1F44C
	emojis["bw.gif"] = 0x1F914
	emojis["sick.gif"] = 0x1F92E
	emojis["smartass.gif"] = 0x1F44B
	emojis["thx.gif"] = 0x1F44D
	emojis["thumbsup.gif"] = 0x1F44E
	emojis["thumbsup.gif"] = 0x1F44E
	emojis["weep.gif"] = 0x1F622
	emojis["tease.gif"] = 0x1F61B
	emojis["grin.gif"] = 0x1F603
	emojis["dp.gif"] = 0x1F92A
	emojis["cry.gif"] = 0x1F62D
	emojis["fein.gif"] = 0x1F606
	emojis["dance3.gif"] = 0x1F483
	emojis["vogelzeig.gif"] = 0x1F926
	emojis["blush.gif"] = 0x1F605
	emojis["jippie.gif"] = 0xFFFD
	emojis["abgelehnt.gif"] = 0xFFFD
	emojis["abinsbett.gif"] = 0xFFFD
	emojis["achlass.gif"] = 0xFFFD
	emojis["afk.gif"] = 0xFFFD
	emojis["augen.GIF"] = 0xFFFD
	emojis["bad.gif"] = 0xFFFD
	emojis["baehh.gif"] = 0xFFFD
	emojis["baehh.gif"] = 0xFFFD
	emojis["bahnhof.gif"] = 0xFFFD
	emojis["banane.gif"] = 0xFFFD
	emojis["binwech.gif"] = 0xFFFD
	emojis["welcome.gif"] = 0xFFFD
	emojis["brille.GIF"] = 0xFFFD
	emojis["ciao.gif"] = 0xFFFD
	emojis["cool.gif"] = 0xFFFD
	emojis["pro.gif"] = 0xFFFD
	emojis["pro.gif"] = 0xFFFD
	emojis["contra.gif"] = 0xFFFD
	emojis["dance.gif"] = 0xFFFD
	emojis["dance2.gif"] = 0xFFFD
	emojis["danke.gif"] = 0xFFFD
	emojis["denken.gif"] = 0xFFFD
	emojis["desnemma.gif"] = 0xFFFD
	emojis["dudu.gif"] = 0xFFFD
	emojis["er.gif"] = 0xFFFD
	emojis["essen.gif"] = 0xFFFD
	emojis["flieg.gif"] = 0xFFFD
	emojis["whistle.gif"] = 0xFFFD
	emojis["whistle.gif"] = 0xFFFD
	emojis["fluestern.gif"] = 0xFFFD
	emojis["fluestern.gif"] = 0xFFFD
	emojis["freu.gif"] = 0xFFFD
	emojis["freu2.gif"] = 0xFFFD
	emojis["hupps.gif"] = 0xFFFD
	emojis["ck.gif"] = 0xFFFD
	emojis["gespraech.gif"] = 0xFFFD
	emojis["gespraech.gif"] = 0xFFFD
	emojis["girlsfriends.gif"] = 0xFFFD
	emojis["gutenacht.GIF"] = 0xFFFD
	emojis["habenwill.gif"] = 0xFFFD
	emojis["hallo.gif"] = 0xFFFD
	emojis["hallo2.gif"] = 0xFFFD
	emojis["hallo3.gif"] = 0xFFFD
	emojis["heul.gif"] = 0xFFFD
	emojis["hi.gif"] = 0xFFFD
	emojis["hi5.gif"] = 0xFFFD
	emojis["hihi.gif"] = 0xFFFD
	emojis["hmmm.GIF"] = 0xFFFD
	emojis["hops.gif"] = 0xFFFD
	emojis["huebsch.gif"] = 0xFFFD
	emojis["huebsch.gif"] = 0xFFFD
	emojis["huhuh.gif"] = 0xFFFD
	emojis["huhu.gif"] = 0xFFFD
	emojis["huldig.gif"] = 0xFFFD
	emojis["kotz.gif"] = 0xFFFD
	emojis["huepf.gif"] = 0xFFFD
	emojis["huepf.gif"] = 0xFFFD
	emojis["ich.gif"] = 0xFFFD
	emojis["ich neee.gif"] = 0xFFFD
	emojis["ichwarsnet.gif"] = 0xFFFD
	emojis["jaa.gif"] = 0xFFFD
	emojis["jippi.gif"] = 0xFFFD
	emojis["kaffee.gif"] = 0xFFFD
	emojis["klaps.gif"] = 0xFFFD
	emojis["klatschen1.gif"] = 0xFFFD
	emojis["kukuck.gif"] = 0xFFFD
	emojis["kizz.gif"] = 0xFFFD
	emojis["kuss.gif"] = 0xFFFD
	emojis["langweil.gif"] = 0xFFFD
	emojis["lieb.gif"] = 0xFFFD
	emojis["lieb2.gif"] = 0xFFFD
	emojis["lol.gif"] = 0xFFFD
	emojis["lol2.gif"] = 0xFFFD
	emojis["lol3.gif"] = 0xFFFD
	emojis["lol4.gif"] = 0xFFFD
	emojis["lol.gif"] = 0xFFFD
	emojis["maus.gif"] = 0xFFFD
	emojis["merci.gif"] = 0xFFFD
	emojis["mist.gif"] = 0xFFFD
	emojis["moin.gif"] = 0xFFFD
	emojis["na.gif"] = 0xFFFD
	emojis["nachti.gif"] = 0xFFFD
	emojis["necken.gif"] = 0xFFFD
	emojis["necken2.gif"] = 0xFFFD
	emojis["nimmdas.gif"] = 0xFFFD
	emojis["nimmdas2.gif"] = 0xFFFD
	emojis["no.gif"] = 0xFFFD
	emojis["nochda.gif"] = 0xFFFD
	emojis["ohnein.gif"] = 0xFFFD
	emojis["ok.gif"] = 0xFFFD
	emojis["oops.GIF"] = 0xFFFD
	emojis["plem.gif"] = 0xFFFD
	emojis["plot.gif"] = 0xFFFD
	emojis["pn.gif"] = 0xFFFD
	emojis["pssst.GIF"] = 0xFFFD
	emojis["psst.gif"] = 0xFFFD
	emojis["puh.gif"] = 0xFFFD
	emojis["reingefallen.gif"] = 0xFFFD
	emojis["rose.gif"] = 0xFFFD
	emojis["rotwerd.gif"] = 0xFFFD
	emojis["ruf.gif"] = 0xFFFD
	emojis["schimpfen.gif"] = 0xFFFD
	emojis["schleimer.gif"] = 0xFFFD
	emojis["schmoll.gif"] = 0xFFFD
	emojis["schoki.gif"] = 0xFFFD
	emojis["shifty.gif"] = 0xFFFD
	emojis["sie.gif"] = 0xFFFD
	emojis["siez.gif"] = 0xFFFD
	emojis["smoke.gif"] = 0xFFFD
	emojis["sorry.GIF"] = 0xFFFD
	emojis["spitze.GIF"] = 0xFFFD
	emojis["strike.gif"] = 0xFFFD
	emojis["strip.gif"] = 0xFFFD
	emojis["tel.gif"] = 0xFFFD
	emojis["totlach.gif"] = 0xFFFD
	emojis["totlach2.gif"] = 0xFFFD
	emojis["troesten.gif"] = 0xFFFD
	emojis["versteck.gif"] = 0xFFFD
	emojis["wanne.gif"] = 0xFFFD
	emojis["warichnet.gif"] = 0xFFFD
	emojis["watt.gif"] = 0xFFFD
	emojis["weissnet.gif"] = 0xFFFD
	emojis["wiegeil.gif"] = 0xFFFD
	emojis["willich.gif"] = 0xFFFD
	emojis["willich2.gif"] = 0xFFFD
	emojis["wink.gif"] = 0xFFFD
	emojis["wink2.gif"] = 0xFFFD
	emojis["wave.gif"] = 0xFFFD
	emojis["wave2.gif"] = 0xFFFD
	emojis["zocken.gif"] = 0xFFFD
	emojis["zug.gif"] = 0xFFFD
	emojis["zunge1.GIF"] = 0xFFFD
	emojis["zunge2.gif"] = 0xFFFD
	emojis["zunge3.gif"] = 0xFFFD
	emojis["zungeziehn.gif"] = 0xFFFD
	emojis["zwinker.gif"] = 0xFFFD
	emojis["aa.gif"] = 0xFFFD
	emojis["ahh.gif"] = 0xFFFD
	emojis["angry.gif"] = 0xFFFD
	emojis["angel.gif"] = 0xFFFD
	emojis["ar.gif"] = 0xFFFD
	emojis["as.gif"] = 0xFFFD
	emojis["av.gif"] = 0xFFFD
	emojis["baby.gif"] = 0xFFFD
	emojis["bd.gif"] = 0xFFFD
	emojis["bike.gif"] = 0xFFFD
	emojis["bo.gif"] = 0xFFFD
	emojis["brumm.gif"] = 0xFFFD
	emojis["bu.gif"] = 0xFFFD
	emojis["bz.gif"] = 0xFFFD
	emojis["chicken.gif"] = 0xFFFD
	emojis["ck.gif"] = 0xFFFD
	emojis["closedeyes.gif"] = 0xFFFD
	emojis["cm.gif"] = 0xFFFD
	emojis["cp.gif"] = 0xFFFD
	emojis["dance4.gif"] = 0xFFFD
	emojis["devil.gif"] = 0xFFFD
	emojis["drunk.gif"] = 0xFFFD
	emojis["Ele.gif"] = 0xFFFD
	emojis["fan.gif"] = 0xFFFD
	emojis["besen.gif"] = 0xFFFD
	emojis["zwerge.gif"] = 0xFFFD
	emojis["hello.gif"] = 0xFFFD
	emojis["geek.gif"] = 0xFFFD
	emojis["friends.gif"] = 0xFFFD
	emojis["fun.gif"] = 0xFFFD
	emojis["give_rose.gif"] = 0xFFFD
	emojis["greeting.gif"] = 0xFFFD
	emojis["hmmm.gif"] = 0xFFFD
	emojis["icecream.gif"] = 0xFFFD
	emojis["kiss.gif"] = 0xFFFD
	emojis["kissing2.gif"] = 0xFFFD
	emojis["love.gif"] = 0xFFFD
	emojis["morgen.gif"] = 0xFFFD
	emojis["morning1.gif"] = 0xFFFD
	emojis["nacht.gif"] = 0xFFFD
	emojis["noexpression.gif"] = 0xFFFD
	emojis["ohmy.gif"] = 0xFFFD
	emojis["plane.gif"] = 0xFFFD
	emojis["read.gif"] = 0xFFFD
	emojis["rofl.gif"] = 0xFFFD
	emojis["skate.gif"] = 0xFFFD
	emojis["kasper.gif"] = 0xFFFD
	emojis["smile1.gif"] = 0xFFFD
	emojis["spam.gif"] = 0xFFFD
	emojis["super.gif"] = 0xFFFD
	emojis["thank you.gif"] = 0xFFFD
	emojis["tongue.gif"] = 0xFFFD
	emojis["wizard.gif"] = 0xFFFD
	emojis["wo.gif"] = 0xFFFD
	emojis["yes.gif"] = 0xFFFD
	emojis["FAQ.gif"] = 0xFFFD
	emojis["bye2.gif"] = 0xFFFD
	emojis["sorry.gif"] = 0xFFFD
	emojis["klopp.gif"] = 0xFFFD
	emojis["pup.gif"] = 0xFFFD
	emojis["welle.gif"] = 0xFFFD
	emojis["smile1.gif"] = 0xFFFD
	emojis["grin.gif"] = 0xFFFD
	emojis["tongue.gif"] = 0xFFFD
	emojis["sad.gif"] = 0xFFFD
	emojis["cry.gif"] = 0xFFFD
	emojis["noexpression.gif"] = 0xFFFD
	emojis["bbfriends.gif"] = 0xFFFD
	emojis["bbwink.gif"] = 0xFFFD
	emojis["bbgrin.gif"] = 0xFFFD
	emojis["bbhat-sm.gif"] = 0xFFFD
	emojis["bbhat.gif"] = 0xFFFD
	emojis["lol5.gif"] = 0xFFFD
	emojis["deadhorse.gif"] = 0xFFFD
	emojis["spank.gif"] = 0xFFFD
	emojis["yoji.gif"] = 0xFFFD
	emojis["locked.gif"] = 0xFFFD
	emojis["clown.gif"] = 0xFFFD
	emojis["mml.gif"] = 0xFFFD
	emojis["morepics.gif"] = 0xFFFD
	emojis["rblocked.gif"] = 0xFFFD
	emojis["maxlocked.gif"] = 0xFFFD
	emojis["hslocked.gif"] = 0xFFFD
}

func emojify(s string) string {
	emojiInit()
	var search string

	for image, emoji := range emojis {
		search = "emoji:" + image
		s = strings.Replace(s, search, string(emoji), -1)
	}

	return s
}

func sanitizeJSON(rd io.Reader) ([]byte, error) {
	body, err := ioutil.ReadAll(rd)
	if err != nil {
		return nil, err
	}
	// Replace tabs in the response. Tabs are not allowed in the json standard, but the send it anyway.
	// Probaby a shitty(custom) json encoder
	return bytes.Replace(body, []byte("\t"), []byte("    "), -1), nil
}
