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
	"io/ioutil"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/transform"
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
	// encode the response from iso-8859-1, or the json encoder shits the bed
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
		if bytes.ContainsAny(body, "Die Serverlast ist Momentan zu hoch") {
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

	// encode the message as iso-8859-1, because np doesn't support utf-8 (even for xhr/json stuff)
	charmapEncoder := charmap.ISO8859_1.NewEncoder()
	message, err := charmapEncoder.String(message)
	if err != nil {
		log.Println(err.Error())
		return false, err
	}
	datap.Add("shbox_text", message)

	resp, err := c.postForm(c.buildUrl("shoutx.php", data), datap)
	if err != nil {
		return false, err
	}

	defer resp.Body.Close()
	// encode the response from iso-8859-1, or the json encoder shits the bed
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
	shoutboxRegexp["emoji"], _ = regexp.Compile("<img.*src=\"(/pic/smilies/([^\"]+))\".*>")
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
	emojiCount := 238
	emojis = make(map[string]rune, emojiCount)

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
	emojis["jippie.gif"] = 0x2620
	emojis["abgelehnt.gif"] = 0x2620
	emojis["abinsbett.gif"] = 0x2620
	emojis["achlass.gif"] = 0x2620
	emojis["afk.gif"] = 0x2620
	emojis["augen.GIF"] = 0x2620
	emojis["bad.gif"] = 0x2620
	emojis["baehh.gif"] = 0x2620
	emojis["baehh.gif"] = 0x2620
	emojis["bahnhof.gif"] = 0x2620
	emojis["banane.gif"] = 0x2620
	emojis["binwech.gif"] = 0x2620
	emojis["welcome.gif"] = 0x2620
	emojis["brille.GIF"] = 0x2620
	emojis["ciao.gif"] = 0x2620
	emojis["cool.gif"] = 0x2620
	emojis["pro.gif"] = 0x2620
	emojis["pro.gif"] = 0x2620
	emojis["contra.gif"] = 0x2620
	emojis["dance.gif"] = 0x2620
	emojis["dance2.gif"] = 0x2620
	emojis["danke.gif"] = 0x2620
	emojis["denken.gif"] = 0x2620
	emojis["desnemma.gif"] = 0x2620
	emojis["dudu.gif"] = 0x2620
	emojis["er.gif"] = 0x2620
	emojis["essen.gif"] = 0x2620
	emojis["flieg.gif"] = 0x2620
	emojis["whistle.gif"] = 0x2620
	emojis["whistle.gif"] = 0x2620
	emojis["fluestern.gif"] = 0x2620
	emojis["fluestern.gif"] = 0x2620
	emojis["freu.gif"] = 0x2620
	emojis["freu2.gif"] = 0x2620
	emojis["hupps.gif"] = 0x2620
	emojis["ck.gif"] = 0x2620
	emojis["gespraech.gif"] = 0x2620
	emojis["gespraech.gif"] = 0x2620
	emojis["girlsfriends.gif"] = 0x2620
	emojis["gutenacht.GIF"] = 0x2620
	emojis["habenwill.gif"] = 0x2620
	emojis["hallo.gif"] = 0x2620
	emojis["hallo2.gif"] = 0x2620
	emojis["hallo3.gif"] = 0x2620
	emojis["heul.gif"] = 0x2620
	emojis["hi.gif"] = 0x2620
	emojis["hi5.gif"] = 0x2620
	emojis["hihi.gif"] = 0x2620
	emojis["hmmm.GIF"] = 0x2620
	emojis["hops.gif"] = 0x2620
	emojis["huebsch.gif"] = 0x2620
	emojis["huebsch.gif"] = 0x2620
	emojis["huhuh.gif"] = 0x2620
	emojis["huhu.gif"] = 0x2620
	emojis["huldig.gif"] = 0x2620
	emojis["kotz.gif"] = 0x2620
	emojis["huepf.gif"] = 0x2620
	emojis["huepf.gif"] = 0x2620
	emojis["ich.gif"] = 0x2620
	emojis["ich neee.gif"] = 0x2620
	emojis["ichwarsnet.gif"] = 0x2620
	emojis["jaa.gif"] = 0x2620
	emojis["jippi.gif"] = 0x2620
	emojis["kaffee.gif"] = 0x2620
	emojis["klaps.gif"] = 0x2620
	emojis["klatschen1.gif"] = 0x2620
	emojis["kukuck.gif"] = 0x2620
	emojis["kizz.gif"] = 0x2620
	emojis["kuss.gif"] = 0x2620
	emojis["langweil.gif"] = 0x2620
	emojis["lieb.gif"] = 0x2620
	emojis["lieb2.gif"] = 0x2620
	emojis["lol.gif"] = 0x2620
	emojis["lol2.gif"] = 0x2620
	emojis["lol3.gif"] = 0x2620
	emojis["lol4.gif"] = 0x2620
	emojis["lol.gif"] = 0x2620
	emojis["maus.gif"] = 0x2620
	emojis["merci.gif"] = 0x2620
	emojis["mist.gif"] = 0x2620
	emojis["moin.gif"] = 0x2620
	emojis["na.gif"] = 0x2620
	emojis["nachti.gif"] = 0x2620
	emojis["necken.gif"] = 0x2620
	emojis["necken2.gif"] = 0x2620
	emojis["nimmdas.gif"] = 0x2620
	emojis["nimmdas2.gif"] = 0x2620
	emojis["no.gif"] = 0x2620
	emojis["nochda.gif"] = 0x2620
	emojis["ohnein.gif"] = 0x2620
	emojis["ok.gif"] = 0x2620
	emojis["oops.GIF"] = 0x2620
	emojis["plem.gif"] = 0x2620
	emojis["plot.gif"] = 0x2620
	emojis["pn.gif"] = 0x2620
	emojis["pssst.GIF"] = 0x2620
	emojis["psst.gif"] = 0x2620
	emojis["puh.gif"] = 0x2620
	emojis["reingefallen.gif"] = 0x2620
	emojis["rose.gif"] = 0x2620
	emojis["rotwerd.gif"] = 0x2620
	emojis["ruf.gif"] = 0x2620
	emojis["schimpfen.gif"] = 0x2620
	emojis["schleimer.gif"] = 0x2620
	emojis["schmoll.gif"] = 0x2620
	emojis["schoki.gif"] = 0x2620
	emojis["shifty.gif"] = 0x2620
	emojis["sie.gif"] = 0x2620
	emojis["siez.gif"] = 0x2620
	emojis["smoke.gif"] = 0x2620
	emojis["sorry.GIF"] = 0x2620
	emojis["spitze.GIF"] = 0x2620
	emojis["strike.gif"] = 0x2620
	emojis["strip.gif"] = 0x2620
	emojis["tel.gif"] = 0x2620
	emojis["totlach.gif"] = 0x2620
	emojis["totlach2.gif"] = 0x2620
	emojis["troesten.gif"] = 0x2620
	emojis["versteck.gif"] = 0x2620
	emojis["wanne.gif"] = 0x2620
	emojis["warichnet.gif"] = 0x2620
	emojis["watt.gif"] = 0x2620
	emojis["weissnet.gif"] = 0x2620
	emojis["wiegeil.gif"] = 0x2620
	emojis["willich.gif"] = 0x2620
	emojis["willich2.gif"] = 0x2620
	emojis["wink.gif"] = 0x2620
	emojis["wink2.gif"] = 0x2620
	emojis["wave.gif"] = 0x2620
	emojis["wave2.gif"] = 0x2620
	emojis["zocken.gif"] = 0x2620
	emojis["zug.gif"] = 0x2620
	emojis["zunge1.GIF"] = 0x2620
	emojis["zunge2.gif"] = 0x2620
	emojis["zunge3.gif"] = 0x2620
	emojis["zungeziehn.gif"] = 0x2620
	emojis["zwinker.gif"] = 0x2620
	emojis["aa.gif"] = 0x2620
	emojis["ahh.gif"] = 0x2620
	emojis["angry.gif"] = 0x2620
	emojis["angel.gif"] = 0x2620
	emojis["ar.gif"] = 0x2620
	emojis["as.gif"] = 0x2620
	emojis["av.gif"] = 0x2620
	emojis["baby.gif"] = 0x2620
	emojis["bd.gif"] = 0x2620
	emojis["bike.gif"] = 0x2620
	emojis["bo.gif"] = 0x2620
	emojis["brumm.gif"] = 0x2620
	emojis["bu.gif"] = 0x2620
	emojis["bz.gif"] = 0x2620
	emojis["chicken.gif"] = 0x2620
	emojis["ck.gif"] = 0x2620
	emojis["closedeyes.gif"] = 0x2620
	emojis["cm.gif"] = 0x2620
	emojis["cp.gif"] = 0x2620
	emojis["dance4.gif"] = 0x2620
	emojis["devil.gif"] = 0x2620
	emojis["drunk.gif"] = 0x2620
	emojis["Ele.gif"] = 0x2620
	emojis["fan.gif"] = 0x2620
	emojis["besen.gif"] = 0x2620
	emojis["zwerge.gif"] = 0x2620
	emojis["hello.gif"] = 0x2620
	emojis["geek.gif"] = 0x2620
	emojis["friends.gif"] = 0x2620
	emojis["fun.gif"] = 0x2620
	emojis["give_rose.gif"] = 0x2620
	emojis["greeting.gif"] = 0x2620
	emojis["hmmm.gif"] = 0x2620
	emojis["icecream.gif"] = 0x2620
	emojis["kiss.gif"] = 0x2620
	emojis["kissing2.gif"] = 0x2620
	emojis["love.gif"] = 0x2620
	emojis["morgen.gif"] = 0x2620
	emojis["morning1.gif"] = 0x2620
	emojis["nacht.gif"] = 0x2620
	emojis["noexpression.gif"] = 0x2620
	emojis["ohmy.gif"] = 0x2620
	emojis["plane.gif"] = 0x2620
	emojis["read.gif"] = 0x2620
	emojis["rofl.gif"] = 0x2620
	emojis["skate.gif"] = 0x2620
	emojis["kasper.gif"] = 0x2620
	emojis["smile1.gif"] = 0x2620
	emojis["spam.gif"] = 0x2620
	emojis["super.gif"] = 0x2620
	emojis["thank you.gif"] = 0x2620
	emojis["tongue.gif"] = 0x2620
	emojis["wizard.gif"] = 0x2620
	emojis["wo.gif"] = 0x2620
	emojis["yes.gif"] = 0x2620
	emojis["FAQ.gif"] = 0x2620
	emojis["bye2.gif"] = 0x2620
	emojis["sorry.gif"] = 0x2620
	emojis["klopp.gif"] = 0x2620
	emojis["pup.gif"] = 0x2620
	emojis["welle.gif"] = 0x2620
	emojis["smile1.gif"] = 0x2620
	emojis["grin.gif"] = 0x2620
	emojis["tongue.gif"] = 0x2620
	emojis["sad.gif"] = 0x2620
	emojis["cry.gif"] = 0x2620
	emojis["noexpression.gif"] = 0x2620
	emojis["bbfriends.gif"] = 0x2620
	emojis["bbwink.gif"] = 0x2620
	emojis["bbgrin.gif"] = 0x2620
	emojis["bbhat-sm.gif"] = 0x2620
	emojis["bbhat.gif"] = 0x2620
	emojis["lol5.gif"] = 0x2620
	emojis["deadhorse.gif"] = 0x2620
	emojis["spank.gif"] = 0x2620
	emojis["yoji.gif"] = 0x2620
	emojis["locked.gif"] = 0x2620
	emojis["clown.gif"] = 0x2620
	emojis["mml.gif"] = 0x2620
	emojis["morepics.gif"] = 0x2620
	emojis["rblocked.gif"] = 0x2620
	emojis["maxlocked.gif"] = 0x2620
	emojis["hslocked.gif"] = 0x2620
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
