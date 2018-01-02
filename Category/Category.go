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

package Category

import (
	"errors"
)

var categories map[int]string

func initCategories() {
	if len(categories) > 0 {
		return
	}
	categories = make(map[int]string, 28)
	categories[1] = "A-book"
	categories[2] = "Album/Sampler"
	categories[3] = "Musik Pack"
	categories[4] = "Musik DVD/Vids"
	categories[5] = "Doku HD"
	categories[6] = "Doku HD Pack"
	categories[7] = "Doku SD"
	categories[8] = "Doku SD Pack"
	categories[9] = "Nintendo"
	categories[10] = "PC"
	categories[11] = "PlayStation"
	categories[12] = "XboX"
	categories[13] = "eBooks"
	categories[14] = "Mobilgeräte"
	categories[15] = "Software"
	categories[16] = "DVDR"
	categories[17] = "1080p"
	categories[18] = "720p"
	categories[19] = "h264/x264"
	categories[20] = "Xvid"
	categories[21] = "XXX"
	categories[22] = "Serie HD"
	categories[23] = "Serie HD Pack"
	categories[24] = "Serie SD"
	categories[25] = "Serie SD Pack"
	categories[26] = "Sport"
	categories[27] = "TV"
	categories[28] = "3-D"
}

func ToInt(name string) (int, error) {
	initCategories()
	for id, val := range categories {
		if val == name {
			return id, nil
		}
	}

	return 0, errors.New("category name not found")
}

func ToString(id int) (string, error) {
	initCategories()
	if val, ok := categories[id]; ok {
		return val, nil
	}

	return "", errors.New("category id not found")
}
