// Copyright 2021 Clayton Craft <clayton@craftyguy.net>
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"fmt"
	"io/ioutil"

	toml "github.com/pelletier/go-toml"
)

type Config struct {
	Socket     string `toml:"socket"`
	OwnerGroup string `toml:"group"`
	Driver     string `toml:"device_driver"`
	DevicePath string `toml:"device_path"`
	BaudRate   int    `toml:"device_baud_rate"`
	CachePath  string `toml:"agps_directory"`
}

func Parse(file string) (c *Config, err error) {
	contents, err := ioutil.ReadFile(file)
	if err != nil {
		err = fmt.Errorf("config.Parse(): %w", err)
		return
	}

	c = &Config{}

	if err = toml.Unmarshal(contents, c); err != nil {
		err = fmt.Errorf("config.Parse(): %w", err)
	}

	return
}
