// Package client provides simple functions to communicate with the socket.
package client

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/abenz1267/elephant/v2/pkg/pb/pb"
)

var socket string

func init() {
	rd := os.Getenv("XDG_RUNTIME_DIR")

	if rd == "" {
		slog.Error("socket", "runtimedir", "XDG_RUNTIME_DIR not set. falling back to /tmp")
		socket = filepath.Join(os.TempDir(), "elephant", "elephant.sock")
	} else {
		socket = filepath.Join(rd, "elephant", "elephant.sock")
	}
}

func Query(data string, async, j bool) {
	v := strings.Split(data, ";")
	if len(v) < 3 {
		panic("query: expected at least 3 semicolon-separated fields: providers;query;maxresults[;exact]")
	}

	maxresults, err := strconv.Atoi(v[2])
	if err != nil {
		panic(fmt.Sprintf("query: invalid maxresults '%s': %v", v[2], err))
	}

	providers := strings.Split(v[0], ",")
	if len(providers) == 0 || (len(providers) == 1 && providers[0] == "") {
		panic("query: no providers specified")
	}

	exact := false
	if len(v) > 3 {
		// treat 4th element as exactsearch boolean ("true" / "false")
		exact = strings.EqualFold(v[3], "true") || v[3] == "1"
	}

	req := pb.QueryRequest{
		Providers:   providers,
		Query:       v[1],
		Maxresults:  int32(maxresults),
		Exactsearch: exact,
	}

	b, err := json.Marshal(&req)
	if err != nil {
		panic(err)
	}

	conn, err := net.Dial("unix", socket)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	var buffer bytes.Buffer
	buffer.Write([]byte{0})
	buffer.Write([]byte{1})

	lengthBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(lengthBuf, uint32(len(b)))
	buffer.Write(lengthBuf)
	buffer.Write(b)

	_, err = conn.Write(buffer.Bytes())
	if err != nil {
		panic(err)
	}

	reader := bufio.NewReader(conn)

	for {
		header, err := reader.Peek(5)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			panic(err)
		}

		code := header[0]
		length := binary.BigEndian.Uint32(header[1:5])

		// Consume the header + payload now
		msg := make([]byte, 5+length)
		_, err = io.ReadFull(reader, msg)
		if err != nil {
			panic(err)
		}

		// Status frames (done / empty) carry no JSON payload and must be skipped
		if code == done {
			if !async { // end of synchronous query
				break
			}
			// async mode: continue; may receive further updates
			continue
		}
		if code == empty { // no results
			continue
		}

		if code != 0 && code != 1 { // data frames are 0 (regular) or 1 (async)
			panic("invalid protocol prefix")
		}

		payload := msg[5:]
		if len(payload) == 0 {
			// Defensive: shouldn't happen for data frames
			continue
		}

		resp := &pb.QueryResponse{}
		if err := json.Unmarshal(payload, resp); err != nil {
			panic(err)
		}

		if !j {
			fmt.Println(resp)
		} else {
			out, err := json.Marshal(resp)
			if err != nil {
				panic(err)
			}
			fmt.Println(string(out))
		}
	}
}
