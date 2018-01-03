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
	"io/ioutil"
	"net/url"
	"strings"
)

func CommentWrite(c *Connection, id int64, message string) (bool, error) {
	c.assureLogin()

	data := url.Values{}
	data.Add("tid", fmt.Sprintf("%d", id))
	data.Add("text", message)
	resp, err := c.postForm(c.buildUrl("comment.php", url.Values{"action": {"add"}}), data)
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
		return false, errors.New("error at irrenhaus")
	}

	return true, nil
}
