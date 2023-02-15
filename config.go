/*
	GOPP - Postfix policy written in Go
	Configuration processing
	by aadz, 2014
*/

package main

import (
	"fmt"
	"hash/crc64"
	"log"
	"log/syslog"
	"net"
	"os"
	"os/user"
	"strconv"
	"syscall"
	"time"

	"gopkg.in/ini.v1"
)

// Global vars
var (
	_cfg = map[string]string{
		"debug":               "yes",
		"grey_listing":        "no",
		"grey_list_delay":     "300",
		"greylist_exceptions": "-none-",
		"grey_list_expire":    "14400",
		"grey_list_store":     "internal",
		"listen_ip":           "127.0.0.1",
		"listen_port":         "10033",
		"log":                 "syslog",
		"memcached_servers":   "127.0.0.1:11211",
		"stat_interval":       "0",
		"user":                "-none-",
	}
	_local_ip_addrs map[string]bool
)

func apply_cfg(initial bool, new_cfg map[string]string) {
	// Set dubug log at first
	d, found := new_cfg["debug"]
	if found && _cfg["debug"] != d {
		switch d {
		case "yes":
			LOG_DEBUG = true
			_cfg["debug"] = d
		case "no":
			LOG_DEBUG = false
			_cfg["debug"] = d
		default:
			logDebug(fmt.Sprintf("Unknown setting %v for parameter debug", d))
		}
	}
	logDebug("Set configuration parameters")

	// Set regular log
	logfile_name, found := new_cfg["log"]
	if found == false {
		logfile_name = _cfg["log"]
	}

	if logfile_name == "syslog" {
		_syslog, err := syslog.New(syslog.LOG_INFO|syslog.LOG_MAIL, PROG_NAME)
		_check(&err)
		log.SetOutput(_syslog)
		log.SetFlags(0)
	} else {
		f, err := os.OpenFile(logfile_name, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0664)
		_check(&err)
		log.SetOutput(f)
	}

	for par, val := range new_cfg {
		if _cfg[par] != val {
			logDebug("New configuration value:", par, val)
		}

		switch par {
		case "grey_listing":
			if _cfg[par] != val {
				_cfg[par] = val
			}
			switch val {
			case "yes":
				GREYLIST = true
			case "no":
				GREYLIST = false
			default:
				_log(fmt.Sprintf("unknown setting %v for parameter grey_listing", val))
			}
		case "grey_list_delay":
			sec, err := strconv.Atoi(val)
			if err != nil {
				_log(fmt.Sprintf("incorrect setting %v for parameter grey_list_delay", val))
			} else {
				_cfg[par] = val
				GREYLIST_DELAY = int64(sec)
			}
		case "grey_list_expire":
			sec, err := strconv.Atoi(val)
			if err != nil {
				_log(fmt.Sprintf("incorrect setting %v for parameter grey_list_expire", val))
			} else {
				_cfg[par] = val
				GREYLIST_EXPIRE = int64(sec)
			}
		case "grey_list_store":
			switch val {
			case "internal", "memcached":
				_cfg[par] = val
			default:
				logDebug(fmt.Sprintf("Unknown setting %v for parameter grey_list_store", val))
			}
		case "listen_ip":
			_cfg[par] = val
		case "listen_port":
			_cfg[par] = val
		case "memcached_servers":
			if _cfg[par] != val {
				_cfg[par] = val
			}
		case "stat_interval":
			seconds, err := strconv.Atoi(val)
			if err != nil {
				_log("incorrect value for stat_interwal:", val)
				STAT_INTERVAL = 0
			} else {
				_cfg[par] = val
				STAT_INTERVAL = time.Duration(time.Duration(seconds) * time.Second)
				logDebug("STAT_INTERVAL set to", seconds)
			}
		case "user":
			/* 	This option does not work in Linux since Go v.1,4.
			https://golang.org/doc/go1.4 says: In the syscall package's implementation
			on Linux, the Setuid and Setgid have been disabled because those system
			calls operate on the calling thread, not the whole process, which is
			different from other platforms and not the expected result.
			*/
			if _cfg["user"] != val && val != "-none-" {
				if initial {
					uid, err := strconv.Atoi(val)
					if err != nil {
						usr, err := user.Lookup(val)
						if err == nil {
							uid, err = strconv.Atoi(usr.Uid)
						} else {
							logDebug("Cannot find UID for", val, ":", err)
						}
					}
					err = syscall.Setuid(uid)
					_check(&err)
					logDebug("UID set to", uid)
				}
			}
			_cfg[par] = val
		}
	}

	if GREYLIST {
		//CRC64_TABLE = crc64.MakeTable(0x42F0E1EBA9EA3693)
		CRC64_TABLE = crc64.MakeTable(crc64.ECMA)
		_local_ip_addrs = get_local_ips()

		if _cfg["grey_list_store"] == "memcached" {
			set_mc_client()
		} else if _cfg["grey_list_store"] == "internal" {
			go clean_grey_map()
		}
	}

	go StatsCollector()
}

func get_local_ips() (addresses map[string]bool) {
	addresses = make(map[string]bool)

	interfaces, err := net.Interfaces()
	if err != nil {
		_log(fmt.Sprintf("Cannot get interfaces list: %v", err))
		return
	}

	for _, i := range interfaces {
		addr, err := i.Addrs()
		if err != nil {
			_log(fmt.Sprintf("Cannot get addresses of %v: %v", i.Name, err))
		}
		for _, j := range addr {
			ipAddr, _, _ := net.ParseCIDR(fmt.Sprintf("%v", j))
			//fmt.Println(addrStr)
			addresses[ipAddr.String()] = true
		}
	}

	if LOG_DEBUG {
		var addrsStr string
		for k, _ := range addresses {
			addrsStr += " " + k
		}
		logDebug(fmt.Sprintf("local IP addresses on the host excluded from greylist check:%v", addrsStr))
	}
	return
}

func readConfig() {
	var new_cfg = make(map[string]string)

	cfg, err := ini.Load(_cfg_file_name)
	if err != nil {
		_log("Cannot read configuration file:", err)
		os.Exit(1)
	}

	keys := cfg.Section("").KeyStrings()

	for _, par := range keys {
		val := cfg.Section("").Key(par).String()

		//check if parameter is alowed
		//logDebug(fmt.Sprintf("Got cfg param %v:%v", par, val))

		if len(par) > 0 && len(val) > 0 {
			new_cfg[par] = val
		}
	}

	apply_cfg(true, new_cfg)
	_log(fmt.Sprintf("%v %v started, configuration read from %v", PROG_NAME, VERSION, _cfg_file_name))
}
