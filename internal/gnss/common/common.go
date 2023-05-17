// Copyright 2023 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package common

import (
	"sync"
)

// A simple ref counting type
type Reference struct {
	sync.Mutex
	count int
}

func (r *Reference) Add() {
	r.Lock()
	defer r.Unlock()

	r.count += 1
}

func (r *Reference) Remove() {
	r.Lock()
	defer r.Unlock()

	if r.count < 1 {
		return
	}

	r.count -= 1
}

func (r *Reference) Count() int {
	r.Lock()
	defer r.Unlock()

	return r.count
}
