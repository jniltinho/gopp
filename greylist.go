package main

import (
	"fmt"
	"hash/crc64"
	"strconv"
	str "strings"
	"time"

	"github.com/bradfitz/gomemcache/memcache"
)

func check_grey(reqMap map[string]string) string {
	var client_address = reqMap["client_address"]
	var recipient = reqMap["recipient"]
	var sender = reqMap["sender"]

	// Skip checking if client has an IP address local for our host
	if _local_ip_addrs[client_address] {
		return DEFAULT_ACTION
	}

	msg_key := crc64.Checksum([]byte(str.ToLower(sender+recipient)+client_address),
		CRC64_TABLE)

	if LOG_DEBUG {
		qid := "" // Queue ID can be empty in policy request, log it if presented.
		if len(reqMap["queue_id"]) > 0 {
			qid = reqMap["queue_id"] + ": "
		}
		_log(fmt.Sprintf("%v grey list check: client %v, sender %v, recipient %v, checksum %x", qid, client_address, sender, recipient, msg_key))
	}

	switch _cfg["grey_list_store"] {
	case "internal":
		return check_grey_internal(msg_key)
	case "memcached":
		return check_grey_memcached(fmt.Sprintf("%v%x", GREYLIST_PREFIX, msg_key))
	}
	panic(fmt.Errorf("Unknown greylist storage `%v'", _cfg["grey_list_store"]))
}

func check_grey_internal(key uint64) string {
	now := int64(time.Now().Unix())
	var action string
	var delta int64

	_grey_map_mutex.Lock()
	try_time, found := _grey_map[key]
	_grey_map_mutex.Unlock()
	if found {
		// message key is already seen, so check time the key was added
		delta = now - try_time
		if delta > GREYLIST_EXPIRE {
			found = false
		} else if delta > GREYLIST_DELAY {
			action = DEFAULT_ACTION
		}
		if LOG_DEBUG {
			logDebug(fmt.Sprintf("now:%v, try_time:%v, GREYLIST_DELAY:%v, delta:%v",
				now, try_time, GREYLIST_DELAY, delta))
		}
	}
	if !found {
		_grey_map_mutex.Lock()
		_grey_map[key] = now
		_grey_map_mutex.Unlock()
		delta = 0
	}
	wait_time := GREYLIST_DELAY - delta
	if action != DEFAULT_ACTION && wait_time > 0 {
		action = fmt.Sprintf(GREYLIST_DEFER_ACTION, wait_time)
	}
	return action
}

func check_grey_memcached(key string) string {
	now := time.Now().Unix()
	var action string
	var delta int64

	it := mc_get(key)
	logDebug(fmt.Sprintf("Got from memcache: %v", it))

	if it == nil {
		if !mc_set(key, strconv.FormatInt(now, 10), GREYLIST_EXPIRE) {
			_log("cannot set memcache item")
			action = DEFAULT_ACTION
		}
		delta = GREYLIST_DELAY
	} else {
		logDebug(fmt.Sprintf("Got memcache item: Key:%v, Value:%v (%v)", it.Key, it.Value, string(it.Value)))
		try_time, err := strconv.ParseInt(string(it.Value), 10, 0)

		if err != nil {
			_log(fmt.Sprintf("cannot convert %v to int: %v", it.Value, err))
			action = DEFAULT_ACTION
		}

		delta = GREYLIST_DELAY - (now - try_time)
		if delta <= 0 {
			action = DEFAULT_ACTION
		}
		logDebug(fmt.Sprintf("now:%v, try_time:%v, GREYLIST_DELAY:%v, delta:%v", now, try_time, GREYLIST_DELAY, delta))
	}

	if action != DEFAULT_ACTION {
		action = fmt.Sprintf(GREYLIST_DEFER_ACTION, delta)
	}
	return action
}

func check_RCPT(rMap map[string]string) string {
	logDebug("Check on RCPT state")

	if GREYLIST {
		res := check_grey(rMap)
		if res != DEFAULT_ACTION {
			return res
		}
	}
	return DEFAULT_ACTION
}

// GOROUTINE: creates and then periodically checks internal grey list
func clean_grey_map() {
	_mutex.Lock()
	_, found := _go_routines_run["clean_grey_map"]
	_mutex.Unlock()
	if found { // already run
		return
	} else {
		_mutex.Lock()
		_go_routines_run["clean_grey_map"] = 1
		_mutex.Unlock()
		logDebug("Starting _grey_map cleaner")
	}

	for {
		if _cfg["grey_list_store"] != "internal" {
			// make greylist map empty
			_grey_map_mutex.Lock()
			_grey_map = make(map[uint64]int64)
			_grey_map_mutex.Unlock()

			_mutex.Lock()
			delete(_go_routines_run, "clean_grey_map")
			_mutex.Unlock()

			return
		}

		time.Sleep(CLEANER_INTERVAL)

		now := time.Now().Unix()
		deleted := 0

		_grey_map_mutex.Lock()
		start_time := time.Now()
		for key, val := range _grey_map {
			if now-val > GREYLIST_EXPIRE {
				delete(_grey_map, key)
				deleted++
			}
		}
		_grey_map_mutex.Unlock()
		_log(fmt.Sprintf("internal greylist cleaner: %v greylist entries deleted in %v", deleted, time.Now().Sub(start_time)))
	}
}

func set_mc_client() {
	// define new memcached client
	srv := str.Split(_cfg["memcached_servers"], ",")
	for i, s := range srv {
		srv[i] = str.Trim(s, " \t")
	}
	_mc = memcache.New(srv...)
}

func mc_get(key string) *memcache.Item {
	var it *memcache.Item
	_memcache_mutex.Lock()
	it, err := _mc.Get(key)
	_memcache_mutex.Unlock()
	if err != nil && err != memcache.ErrCacheMiss {
		logDebug(err)
	}
	return it
}

func mc_set(key string, val string, exp int64) bool {
	it := memcache.Item{Key: key, Value: []byte(val), Expiration: int32(exp)}
	logDebug("mc_set(): new memcache item:", it)

	_memcache_mutex.Lock()
	err := _mc.Set(&it)
	_memcache_mutex.Unlock()
	if err != nil {
		_log(err)
		return false
	}
	return true
}
