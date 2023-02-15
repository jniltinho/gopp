package main

import (
	"flag"
	"fmt"
	"net"
	"os"
)

func init() {
	var err error

	_PID = os.Getpid()
	_hostname, err = os.Hostname()
	if err != nil {
		_log_debug("Cannot find the host name, 'localhost' assumed")
		_hostname = "localhost"
	}

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %v -c CONFIG_FILE\n\n", PROG_NAME)
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "  -h\tShow this help page.\n")
	}

	command_line_get()
	read_config()
}

func main() {
	laddr := _cfg["listen_ip"] + ":" + _cfg["listen_port"]
	l, err := net.Listen("tcp", laddr)
	_check(&err)    // or die
	defer l.Close() // Close the listener when the application closes.

	if _cfg["grey_listing"] == "yes" {
		_local_ip_addrs = get_local_ips()
	}
	_log_debug("listening on " + laddr)

	for {
		conn, err := l.Accept()
		_check(&err) // or die
		_log("connect from ", conn.RemoteAddr())
		_conn_cnt_mutex.Lock()
		_conn_cnt++
		_conn_cnt_mutex.Unlock()
		go handle_requests(conn)
	}
}
