package main

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"sync"
)

const url = "http://icanhazip.com"

var EOF = errors.New("EOF")

var ErrShortWrite = errors.New("short write")

type Log struct {
	RemoteIp   string `json:"r_ip"`
	RemotePort string `json:"r_port"`
	AgentIp    string `json:"a_ip"`
	AgentPort  string `json:"a_port"`
	Payload    string `json:"payload"`
}

type forwardServer struct {
	directorip string
	pubip      string
}

func (fs *forwardServer) Handle(conn1 net.Conn, addr string) {
	conn2, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Errorf("Failed to connect TCP %s", err.Error())
		return
	}
	fs.forward(conn1, conn2)
}

func (fs *forwardServer) forward(conn1 net.Conn, conn2 net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func(src net.Conn, dst net.Conn) {
		//defer conn2.Close()
		defer dst.Close()
		defer wg.Done()
		fs.copyToDirector(dst, src)
	}(conn1, conn2)

	go func(src net.Conn, dst net.Conn) {
		//defer conn1.Close()
		defer dst.Close()
		defer wg.Done()
		fs.copyToAttacker(dst, src)
	}(conn2, conn1)
	wg.Wait()
}

func (fs *forwardServer) copyToDirector(dst net.Conn, src net.Conn) (written int64, err error) {
	payload := new(bytes.Buffer)
	defer func() {
		go fs.log(src, dst, payload.Bytes())
	}()

	buf := make([]byte, 32*1024)
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			_, e := payload.Write(buf[0:nr])
			if e != nil {
				fmt.Errorf("Payload Write Error: %s", err.Error())
			}
			nw, ew := dst.Write(buf[0:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				err = ew
				break
			}
			if nr != nw {
				err = ErrShortWrite
				break
			}
		}
		if err == EOF {
			break
		}
		if er != nil {
			err = er
			break
		}
	}
	return written, err
}

func (fs *forwardServer) copyToAttacker(dst net.Conn, src net.Conn) (written int64, err error) {
	buf := make([]byte, 32*1024)
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			payload := bytes.ReplaceAll(buf[0:nr], []byte(fs.directorip), []byte(fs.pubip))
			nw, ew := dst.Write(payload)
			if nw > 0 {
				written += int64(nw)
			}
			if ew != nil {
				err = ew
				break
			}
			//if nr != nw {
			//	err = ErrShortWrite
			//	break
			//}
		}
		if err == EOF {
			break
		}
		if er != nil {
			err = er
			break
		}
	}
	return written, err
}

func (fs *forwardServer) log(conn1 net.Conn, conn2 net.Conn, data []byte) {
	remoteAddr := conn1.RemoteAddr().String()
	remoteIp, remotePort, _ := net.SplitHostPort(remoteAddr)

	agentAddr := conn1.LocalAddr().String()
	agentIp, agentPort, _ := net.SplitHostPort(agentAddr)

	dataZlib := fs.compress(data)
	payload := fs.base64(dataZlib)

	log := &Log{
		RemoteIp:   remoteIp,
		RemotePort: remotePort,
		AgentIp:    agentIp,
		AgentPort:  agentPort,
		Payload:    payload,
	}

	b, err := json.Marshal(log)
	if err != nil {
		fmt.Errorf("JSON Marshal Error: %s", err.Error())
		return
	}

	fmt.Println(string(b))
}

func (fs *forwardServer) compress(source []byte) []byte {
	var in bytes.Buffer
	w := zlib.NewWriter(&in)
	w.Write(source)
	w.Close()
	return in.Bytes()
}

func (fs *forwardServer) base64(dataZlib []byte) string {
	return base64.StdEncoding.EncodeToString(dataZlib)
}

func PublicIp() (ip string, err error) {
	client := &http.Client{}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("User-Agent", "curl")
	resp, _ := client.Do(req)
	defer resp.Body.Close()
	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return ip, err
	}
	ip = string(bytes[:len(bytes)-1])
	return ip, nil
}

func main() {
	var port string
	flag.StringVar(&port, "port", "", "port listen")
	var addr string
	flag.StringVar(&addr, "addr", "", "forward addr")
	flag.Parse()
	flag.Usage()

	directorip, _, err := net.SplitHostPort(addr)
	if err != nil {
		return
	}

	pubip, err := PublicIp()
	if err != nil {
		return
	}

	fs := forwardServer{
		directorip: directorip,
		pubip:      pubip,
	}
	l, err := net.Listen("tcp", ":"+port)
	if err != nil {
		fmt.Errorf(err.Error())
	}
	defer l.Close()

	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println("Error Accepting")
		}
		fs.Handle(conn, addr)
	}
}
