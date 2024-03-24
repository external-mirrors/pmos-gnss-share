gnss-share is an app that facilitates sharing GNSS location data with multiple
clients, while providing a way to perform device-specific setup beforehand. For
some devices, it can also manage loading and storing A-GPS data.

This is meant to replace things like `gpsd`, and `gps-share`, and work together
with `geoclue`* or other clients that support fetching NMEA location data over
sockets.

# Configuration

`gnss-share.conf` can be used to change the listening socket, group owner for
socket, and other options. See `man gnss-share.conf` for details.

The application looks for this file in either the
current working directory, or in `/etc/gnss-share.conf`.

# Usage

See `man 1 gnss-share`.

# Installation

### Dependencies:

- Go

- ModemManager >=1.23.2 (if using the `mm` driver)

Build the `gnss-share` application with:

```
$ go build ./cmd/gnss-share
```

# Development

### New GNSS device support

Support for additional gnss devices can be added by implementing the
`gnss_driver` interface, see `internal/gnss/gnss.go` for specifics.
