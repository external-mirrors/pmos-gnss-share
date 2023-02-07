gnss-share(1) "gnss-share"

# NAME

gnss-share

# DESCRIPTION

gnss-share is an app that facilitates sharing GNSS location data with multiple
clients, while providing a way to perform device-specific setup beforehand. For
some devices, it can also manage loading and storing A-GPS data.

# CONCEPTS

gnss-share was originally created to allow multiplexing GNSS data from a single
device to multiple clients, and to facilitate loading/storing A-GPS data to and
from the device. gnss-share effectively acts as a proxy between the GNSS device
and the clients, which allows controlling when and what location data clients
receive, as well as giving an opportunity to set time, rough location, etc on
the device to improve lock speed. Some external signals can be used to control
gnss-share, see *SIGNALS* for more information.

# COMMANDS

*[none]*
	The default behavior if no command is specified is to run in server mode.

*store*
	Store almanac and ephemerides data and quit.

*load*
	Load almanac and ephemerides data and quit.

# OPTIONS

*-c* <path>
	Path to the configuration file to use. Default: /etc/gnss-share.conf

*-d*
	Enable printing debug output.

*-h*
	Print help and exit.

# SIGNALS

In addition to the command line options, this application will respond to the
following signals when in "server" mode:

*SIGUSR1*
	The application will load AGPS data from the directory *agps_directory*
	specified in the configuration file, and continue running afterward.

*SIGUSR2*
	The application will store AGPS data to the directory *agps_directory*
	specified in the configuration file, and continue running afterward.

# SEE ALSO
	*gnss-share.conf*(5)

# AUTHORS

*Clayton Craft* <clayton@craftyguy.net>
