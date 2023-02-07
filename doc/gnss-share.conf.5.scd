gnss-share.conf(5) "gnss-share.conf"

# DESCRIPTION

gnss-share.conf contains various options for configuring gnss-share. The file
format is TOML.

# OPTIONS
*socket="<path>"*
	Socket to sent NMEA location to, e.g. "/var/run/gnss-share.sock"

*group="<string>"*
	Group to set as owner for the socket.

*device_driver="<string>"*
	GPS device driver to use. Supported values: stm, stm_serial

*device_path="<string>"*
	Path to GPS device to use, e.g. "/dev/gnss0"

*device_baud_rate=<int>*
	Baud rate for GPS serial device, e.g. "9600". Only used if the device driver
	uses a serial interface.

*agps_directory="<path>"*
	Directory to load/store almanac and ephemeris data

# SEE ALSO
	*gnss-share*(1)

# AUTHORS

*Clayton Craft* <clayton@craftyguy.net>
