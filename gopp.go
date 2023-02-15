/*
	GOPP - Postfix policy written in Go
	by aadz, 2014
*/

package main

import (
	"flag"
	"fmt"
	"hash/crc64"
	"io"
	"log"
	"log/syslog"
	"net"
	"os"
	"runtime"
	str "strings"
	"sync"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
)

const (
	PROG_NAME             string        = "gopp"
	VERSION               string        = "v0.2.4-21-gfd5532f"
	DEFAULT_CFG_FNAME     string        = "/etc/postfix/gopp.cfg"
	DEFAULT_ACTION        string        = "DUNNO"
	GREYLIST_DEFER_ACTION string        = "DEFER_IF_PERMIT Greylisted for %v seconds please try again"
	GREYLIST_PREFIX       string        = "GrlstPlc"
	CLEANER_INTERVAL      time.Duration = 300 * time.Second
)

// Global vars
var (
	_cfg_file_name     string
	_conn_cnt          uint
	_hostname          string
	_go_routines_run   map[string]byte  = make(map[string]byte)
	_grey_map          map[uint64]int64 = make(map[uint64]int64)
	_mc                *memcache.Client
	_PID               int
	_requests_cnt      uint
	_requests_duration time.Duration = 0
	_syslog            *syslog.Writer
	CRC64_TABLE        *crc64.Table
	GREYLIST           bool          = false
	GREYLIST_DELAY     int64         = 300
	GREYLIST_EXPIRE    int64         = 14400
	LOG_DEBUG          bool          = true
	STAT_INTERVAL      time.Duration = 0
)

var (
	_mutex                 sync.Mutex
	_conn_cnt_mutex        sync.Mutex // connections counter
	_grey_map_mutex        sync.Mutex
	_memcache_mutex        sync.Mutex
	_requests_cntavg_mutex sync.Mutex // used for _requests_cnt and _requests_duration
)

// Get command line parameters
func cmdLineGet() {
	flag.StringVar(&_cfg_file_name, "c", DEFAULT_CFG_FNAME, "Set configuration file name.")
	flagShortVersion := flag.Bool("v", false, "Show version information and exit.")
	flagVersion := flag.Bool("V", false, "Show version information of the programm and Go runtime, then exit.")
	flag.Parse()

	if len(os.Args) == 1 {
		flag.Usage()
		os.Exit(1)
	}

	if *flagVersion {
		fmt.Println(PROG_NAME, VERSION, runtime.Version())
		os.Exit(0)
	}
	if *flagShortVersion {
		fmt.Println(PROG_NAME, VERSION)
		os.Exit(0)
	}
}

func handleRequests(conn net.Conn) {
	var start_time time.Time
	reqStr := ""     // Postfix request as a string
	EOF := false     // true indicates closed connection
	request_cnt := 0 // request counter for current connection only
	channel := make(chan string)
	defer close(channel)
	defer conn.Close()

	go read_request(conn, channel)
	for { // process incomming policy requests while not EOF
		if EOF {
			break
		}
		for {
			in_str := <-channel
			if in_str == "EOF" {
				EOF = true
				break
			}
			reqStr += in_str

			// if we've got the last part of a Postfix policy request here
			if str.HasSuffix(reqStr, "\n\n") {
				request_cnt++
				if LOG_DEBUG {
					logDebug(fmt.Sprintf("Policy request from %v %v (%v bytes)", conn.RemoteAddr(), request_cnt, len(reqStr)))
					logDebug(reqStr)
				}
				if STAT_INTERVAL > 0 {
					start_time = time.Now()
				}
				msg_map := parse_request(&reqStr)
				if len(msg_map) > 0 {
					conn.Write([]byte(fmt.Sprintf("action=%v\n\n", policy_check(msg_map))))
				}
				if STAT_INTERVAL > 0 { // update global counters
					d := time.Now().Sub(start_time)
					_requests_cntavg_mutex.Lock()
					_requests_duration += d
					_requests_cnt++
					_requests_cntavg_mutex.Unlock()
				}
				reqStr = "" // clean reqStr to get new request
			}
		}
	}
	_log(fmt.Sprintf("connection closed from %v after %v req sent",
		conn.RemoteAddr(), request_cnt))
}

func parse_request(pMsg *string) map[string]string {
	req := make(map[string]string)

	for _, line := range str.Split(*pMsg, "\n") {
		if len(line) == 0 {
			break // the end of request
		}
		pv := str.SplitN(line, "=", 2)
		req[pv[0]] = pv[1]
	}
	return req
}

func policy_check(rMap map[string]string) string {
	req_type, ok := rMap["request"]
	if ok == false || req_type != "smtpd_access_policy" {
		_log("policy request type unknown")
		return ""
	}

	action := DEFAULT_ACTION

	switch rMap["protocol_state"] {
	case "RCPT":
		action = check_RCPT(rMap)
	default:
		_log("unknown or unsuported protocol state ", rMap["protocol_state"])
	}
	return action
}

func read_request(conn net.Conn, channel chan string) {
	for {
		buf := make([]byte, 768) // should be enouhg for usual request
		cnt, err := conn.Read(buf)
		if err == io.EOF { // connection closed by client
			channel <- "EOF"
			return
		} else if err != nil {
			_log(fmt.Sprintf("error reading from %v: %v",
				conn.RemoteAddr(), err.Error()))
		}
		channel <- string(buf[0:cnt])
	}
}

func _check(e *error) {
	if *e != nil {
		_log("Fatal:", *e)
		fmt.Println("Fatal:", *e)
		os.Exit(1)
	}
}

func _log(v ...interface{}) {
	log.Print(v...)
	if LOG_DEBUG {
		logDebug(v...)
	}
}

func logDebug(v ...interface{}) {
	if LOG_DEBUG {
		fmt.Printf("%v %v %v[%v]: ", _now(), _hostname, PROG_NAME, _PID)
		fmt.Println(v...)
	}
}

func _now() string {
	return time.Now().Format(time.StampMilli)
}

func StatsCollector() {
	_mutex.Lock()
	_, found := _go_routines_run["StatsCollector"]
	_mutex.Unlock()
	if found || STAT_INTERVAL <= 0 { // already run or need no statistics
		return
	} else { // registration
		_mutex.Lock()
		_go_routines_run["StatsCollector"] = 1
		_mutex.Unlock()
	}
	logDebug("Stats Collector Run")

	var (
		stat_timer                              *time.Timer = time.NewTimer(STAT_INTERVAL)
		conn_cnt, requests_cnt                  uint
		requests_duration, request_avg_duration time.Duration
		str_grey_map_cnt                        string
	)

	prev_ts := time.Now() // timestamp
	for {
		_ = <-stat_timer.C
		ts := time.Now() // timestamp
		stat_timer.Reset(STAT_INTERVAL)
		interval := float32(ts.Sub(prev_ts) / time.Second)
		prev_ts = ts

		// Connections counter
		_conn_cnt_mutex.Lock()
		conn_cnt = _conn_cnt
		_conn_cnt = 0
		_conn_cnt_mutex.Unlock()

		// Requests counter & Average request duration
		_requests_cntavg_mutex.Lock()
		requests_cnt = _requests_cnt
		_requests_cnt = 0
		requests_duration = _requests_duration
		_requests_duration = 0
		_requests_cntavg_mutex.Unlock()

		if requests_cnt > 0 {
			request_avg_duration = requests_duration / time.Duration(requests_cnt)
		} else {
			request_avg_duration = 0
		}

		// Internal grey list records count
		if GREYLIST && _cfg["grey_list_store"] == "internal" {
			_grey_map_mutex.Lock()
			greylisted := len(_grey_map)
			_grey_map_mutex.Unlock()
			str_grey_map_cnt = fmt.Sprintf(", greylisted %v", greylisted)
		}

		_log(fmt.Sprintf("statistics: interval %v, connections %v (%.4f p/s), requests %v (%.4f p/s, %v avg p/req)%v",
			STAT_INTERVAL, conn_cnt, float32(conn_cnt)/interval, requests_cnt,
			float32(requests_cnt)/interval, request_avg_duration, str_grey_map_cnt))
	}
}
