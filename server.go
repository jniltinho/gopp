package main

import (
	"net"
)

func RunServer() {
	laddr := _cfg["listen_ip"] + ":" + _cfg["listen_port"]
	l, err := net.Listen("tcp", laddr)
	_check(&err)    // or die
	defer l.Close() // Close the listener when the application closes.

	if _cfg["grey_listing"] == "yes" {
		_local_ip_addrs = get_local_ips()
	}
	logDebug("listening on " + laddr)

	for {
		conn, err := l.Accept()
		_check(&err) // or die
		_log("connect from ", conn.RemoteAddr())
		_conn_cnt_mutex.Lock()
		_conn_cnt++
		_conn_cnt_mutex.Unlock()
		go handleRequests(conn)
	}
}
