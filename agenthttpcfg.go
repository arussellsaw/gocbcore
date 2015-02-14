package gocouchbaseio

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type configStreamBlock struct {
	Bytes []byte
}

func (i *configStreamBlock) UnmarshalJSON(data []byte) error {
	i.Bytes = make([]byte, len(data))
	copy(i.Bytes, data)
	return nil
}

func (c *Agent) httpConfigStream(address, hostname, bucket string) {
	uri := fmt.Sprintf("%s/pools/default/bucketsStreaming/%s", address, bucket)
	resp, err := c.httpCli.Get(uri)
	if err != nil {
		return
	}

	dec := json.NewDecoder(resp.Body)
	configBlock := new(configStreamBlock)
	for {
		err := dec.Decode(configBlock)
		if err != nil {
			resp.Body.Close()
			return
		}

		bkCfg, err := parseConfig(configBlock.Bytes, hostname)
		if err == nil {
			c.updateConfig(bkCfg)
		}
	}
}

func hostnameFromUri(uri string) string {
	uriInfo, err := url.Parse(uri)
	if err != nil {
		panic("Failed to parse URI to hostname!")
	}
	return strings.Split(uriInfo.Host, ":")[0]
}

func (c *Agent) httpLooper(firstCfgFn func(*cfgBucket, error)) {
	waitPeriod := 20 * time.Second
	maxConnPeriod := 10 * time.Second
	var iterNum uint64 = 1
	iterSawConfig := false
	seenNodes := make(map[string]uint64)
	isFirstTry := true

	logDebugf("HTTP Looper starting.")
	for {
		routingInfo := c.routingInfo.get()

		var pickedSrv string
		for _, srv := range routingInfo.mgmtEpList {
			if seenNodes[srv] >= iterNum {
				continue
			}
			pickedSrv = srv
			break
		}

		logDebugf("Http Picked: %s.", pickedSrv)

		if pickedSrv == "" {
			// All servers have been visited during this iteration
			if isFirstTry {
				logDebugf("Pick Failed.")
				firstCfgFn(nil, &agentError{"Failed to connect to all specified hosts."})
				return
			} else {
				if !iterSawConfig {
					logDebugf("Looper waiting...")
					// Wait for a period before trying again if there was a problem...
					<-time.After(waitPeriod)
				}
				logDebugf("Looping again.")
				// Go to next iteration and try all servers again
				iterNum++
				iterSawConfig = false
				continue
			}
		}

		hostname := hostnameFromUri(pickedSrv)

		logDebugf("HTTP Hostname: %s.", pickedSrv)

		// HTTP request time!
		uri := fmt.Sprintf("%s/pools/default/bucketsStreaming/%s", pickedSrv, c.bucket)
		resp, err := c.httpCli.Get(uri)
		if err != nil {
			logDebugf("Failed to connect to host.")
			return
		}

		logDebugf("Connected.")

		// Autodisconnect eventually
		go func() {
			<-time.After(maxConnPeriod)
			logDebugf("Auto DC!")
			resp.Body.Close()
		}()

		dec := json.NewDecoder(resp.Body)
		configBlock := new(configStreamBlock)
		for {
			err := dec.Decode(configBlock)
			if err != nil {
				resp.Body.Close()
				break
			}

			logDebugf("Got Block.")

			bkCfg, err := parseConfig(configBlock.Bytes, hostname)
			if err != nil {
				resp.Body.Close()
				break
			}

			logDebugf("Got Config.")

			iterSawConfig = true
			if isFirstTry {
				logDebugf("HTTP Config Init")
				firstCfgFn(bkCfg, nil)
				isFirstTry = false
			} else {
				logDebugf("HTTP Config Update")
				c.updateConfig(bkCfg)
			}
		}

		logDebugf("HTTP, Setting %s to iter %d", pickedSrv, iterNum)
		seenNodes[pickedSrv] = iterNum
	}
}
