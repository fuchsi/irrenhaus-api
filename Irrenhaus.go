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
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var DEBUG = false

type Connection struct {
	url     string
	cookies Cookies

	username string
	password string
	pin      string

	client *http.Client

	userAgent string
}

type Cookies struct {
	Uid      int64
	Pass     string
	Passhash string
}

func NewConnection(url string, username string, password string, pin string) (Connection) {
	c := Connection{url: url, userAgent: "irrenhaus-api client", username: username, password: password, pin: pin}
	c.client = &http.Client{Timeout: time.Second * 10}
	c.cookies = Cookies{Uid: 0, Pass: "", Passhash: ""}
	//c.client.CheckRedirect = redirectHandler
	c.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	return c
}

func (c *Connection) SetUserAgent(userAgent string) {
	c.userAgent = userAgent
}

func (c Connection) GetCookies() (Cookies) {
	return c.cookies
}

func (c *Connection) SetCookies(cookies Cookies) {
	c.cookies = cookies
}

func (c Connection) buildUrl(url string, values url.Values) (string) {
	if url[0] != '/' {
		url = "/" + url
	}
	if len(values) > 0 {
		return c.url + url + "?" + values.Encode()
	}
	return c.url + url
}

func (c *Connection) Login() (error) {
	debugLog("[Login] Logging in")
	resp, err := c.postForm(c.buildUrl("takelogin.php", nil), url.Values{"username": {c.username}, "password": {c.password}, "pin": {c.pin}})

	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	debugRequest(resp, string(body))
	if strings.Contains(string(body), "Anmeldung Gescheitert!") {
		return errors.New("invalid credentials")
	}

	for _, cookie := range resp.Cookies() {
		switch cookie.Name {
		case "uid":
			c.cookies.Uid, _ = strconv.ParseInt(cookie.Value, 10, 64)
		case "pass":
			c.cookies.Pass = cookie.Value
		case "passhash":
			c.cookies.Passhash = cookie.Value
		}
	}

	debugLog("[Login] Logged in")

	return nil
}

func (c Connection) postForm(url string, data url.Values) (resp *http.Response, err error) {
	return c.post(url, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
}

func (c Connection) post(url string, contentType string, body io.Reader) (resp *http.Response, err error) {
	req, err := c.newRequest("POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.client.Do(req)
}

func (c Connection) get(url string) (resp *http.Response, err error) {
	req, err := c.newRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	return c.client.Do(req)
}

func (c Connection) newRequest(method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("UserAgent", c.userAgent)
	if c.cookies.Uid != 0 {
		req.AddCookie(&http.Cookie{Name: "uid", Value: fmt.Sprintf("%d", c.cookies.Uid)})
		req.AddCookie(&http.Cookie{Name: "pass", Value: c.cookies.Pass})
		if c.cookies.Passhash != "" {
			req.AddCookie(&http.Cookie{Name: "passhash", Value: c.cookies.Passhash})
		}
	}

	return req, nil
}

func (c *Connection) assureLogin() (error) {
	resp, err := c.get(c.buildUrl("/my.php", nil))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	debugRequest(resp, string(body))

	respUrl, err := resp.Location()
	if err != nil {
		if err != http.ErrNoLocation {
			return err
		} else {
			//fmt.Println("Response has no location")
			return nil
		}
	}
	if strings.HasPrefix(respUrl.Path, "/login.php") {
		//fmt.Println("Not logged in")
		return c.Login()
	}

	//if strings.Contains(string(body), "Nicht angemeldet!") {
	//	fmt.Println("Not logged in")
	//	return c.Login()
	//}

	return nil
}

func keepLines(s string, n int) string {
	if strings.Count(s, "\n") < 3 {
		return s
	}
	result := strings.Join(strings.Split(s, "\n")[:n], "\n")
	return strings.Replace(result, "\r", "", -1)
}

func debugRequest(resp *http.Response, body string) {
	if !DEBUG {
		return
	}
	log.Printf("> %s %s://%s%s\n", resp.Request.Method, resp.Request.URL.Scheme, resp.Request.Host, resp.Request.URL.RequestURI())
	log.Println("Request:")
	if resp.Request.Method == "POST" {
		for key, value := range resp.Request.Form {
			log.Printf("    %s: %s\n", key, value)
		}
	}

	for key, header := range resp.Request.Header {
		log.Printf("    %s: %s\n", key, header)
	}

	log.Println("Response:")
	log.Println("  Status Code:", resp.StatusCode)
	log.Println("  Status:", resp.Status)
	for key, header := range resp.Header {
		log.Printf("    %s: %s\n", key, header)
	}

	if resp.Header.Get("Content-Type") == "application/x-bittorrent" {
		log.Println("[body ommited]")
	} else {
		log.Println("[body truncated]")
		log.Println(keepLines(body, 3))
	}

	log.Println("")
}

func debugLog(a ...interface{}) {
	if !DEBUG {
		return
	}
	log.Println(a)
}
