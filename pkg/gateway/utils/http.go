// Copyright 2023 Ant Group Co., Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type HTTPParam struct {
	Method       string
	Path         string
	ClusterName  string
	KusciaSource string
	KusciaHost   string
	Headers      map[string]string
}

func ParseURL(url string) (string, string, uint32, string, error) {
	var protocol, hostPort, host, path string
	var port int
	var err error
	if strings.HasPrefix(url, "http://") {
		protocol = "http"
		hostPort = url[7:]
	} else if strings.HasPrefix(url, "https://") {
		protocol = "https"
		hostPort = url[8:]
	} else {
		return protocol, host, uint32(port), path, fmt.Errorf("invalid host: %s", url)
	}

	parts := strings.SplitN(hostPort, "/", 2)
	hostPort = parts[0]
	if len(parts) > 1 {
		path = "/" + parts[1]
	}

	fields := strings.Split(hostPort, ":")
	host = fields[0]
	if len(fields) == 2 {
		if port, err = strconv.Atoi(fields[1]); err != nil {
			return protocol, host, uint32(port), path, err
		}
	} else {
		if protocol == "http" {
			port = 80
		} else {
			port = 443
		}
	}

	return protocol, host, uint32(port), path, nil
}

func DoHTTPWithRetry(in interface{}, out interface{}, hp *HTTPParam, waitTime time.Duration, maxRetryTimes int) error {
	var err error
	for i := 0; i < maxRetryTimes; i++ {
		err = DoHTTP(in, out, hp)
		if err == nil {
			return nil
		}
		time.Sleep(waitTime)
	}

	return fmt.Errorf("request error, retry at maxtimes:%d, path: %s, err:%s", maxRetryTimes, hp.Path, err.Error())
}

type ErrType int

const (
	NewHTTPRequestError ErrType = iota
	InParameterMarshalToJSONError
	OutParameterRunMarshalFromJSONError
	ResponseStatusCodeNotOK
	DoHTTPError
	IOError
)

func DoHTTPWithHandler(in interface{}, out interface{}, hp *HTTPParam, handler func(et ErrType, err error)) {
	var req *http.Request
	var err error
	if hp.Method == http.MethodGet {
		req, err = http.NewRequest(http.MethodGet, InternalServer+hp.Path, nil)
		if err != nil && handler != nil {
			handler(NewHTTPRequestError, err)
			return
		}
	} else {
		inbody, err := json.Marshal(in)
		if err != nil && handler != nil {
			handler(InParameterMarshalToJSONError, err)
			return
		}
		req, err = http.NewRequest(hp.Method, InternalServer+hp.Path, bytes.NewBuffer(inbody))
		if err != nil && handler != nil {
			handler(NewHTTPRequestError, err)
			return
		}
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(fmt.Sprintf("%s-Cluster", ServiceHandshake), hp.ClusterName)
	req.Header.Set("Kuscia-Source", hp.KusciaSource)
	req.Header.Set("kuscia-Host", hp.KusciaHost)
	for key, val := range hp.Headers {
		req.Header.Set(key, val)
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil && handler != nil {
		handler(DoHTTPError, err)
		return
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil && handler != nil {
		handler(IOError, err)
		return
	}

	if resp.StatusCode != http.StatusOK && handler != nil {
		if len(body) > 200 {
			body = body[:200]
		}
		handler(ResponseStatusCodeNotOK, fmt.Errorf("code: %d, message: %s", resp.StatusCode, string(body)))
		return
	}

	if err := json.Unmarshal(body, out); err != nil && handler != nil {
		if len(body) > 200 {
			body = body[:200]
		}
		handler(OutParameterRunMarshalFromJSONError, fmt.Errorf("%s, body:%s", err.Error(), string(body)))
		return
	}
}

func DoHTTP(in interface{}, out interface{}, hp *HTTPParam) error {
	var e error
	DoHTTPWithHandler(in, out, hp, func(et ErrType, err error) {
		switch et {
		case NewHTTPRequestError:
			e = fmt.Errorf("%s new fail:%v", genErrorPrefix(hp), err)
		case InParameterMarshalToJSONError:
			e = fmt.Errorf("%s in parameter marshal to json fail:%v", genErrorPrefix(hp), err)
		case OutParameterRunMarshalFromJSONError:
			e = fmt.Errorf("%s out parameter unmarshal from json fail:%v", genErrorPrefix(hp), err)
		case ResponseStatusCodeNotOK:
			e = fmt.Errorf("%s get code is not ok: %v", genErrorPrefix(hp), err)
		case DoHTTPError:
			e = fmt.Errorf("%s do fail: %v", genErrorPrefix(hp), err)
		case IOError:
			e = fmt.Errorf("%s read body fail: %v", genErrorPrefix(hp), err)
		}
	})
	return e
}

func genErrorPrefix(hp *HTTPParam) string {
	return fmt.Sprintf("request(method:%s path:%s cluster:%s host:%s)", hp.Method, hp.Path, hp.ClusterName, hp.KusciaHost)
}
