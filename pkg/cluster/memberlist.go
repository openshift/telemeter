package cluster

import (
	"fmt"
	"io/ioutil"
	stdlog "log"
	"net"
	"strconv"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/hashicorp/memberlist"
)

type delegate interface {
	memberlist.EventDelegate
	memberlist.Delegate
}

func NewMemberlist(logger log.Logger, name, addr string, secret []byte, verbose bool, d delegate) (*memberlist.Memberlist, error) {
	if len(secret) != 32 {
		return nil, fmt.Errorf("invalid secret size, must be 32 bytes: %d", len(secret))
	}

	host, portString, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("address must be a host:port: %v", err)
	}

	port, err := strconv.Atoi(portString)
	if err != nil {
		return nil, fmt.Errorf("address must be a host:port: %v", err)
	}

	cfg := memberlist.DefaultWANConfig()
	cfg.DelegateProtocolVersion = protocolVersion
	cfg.DelegateProtocolMax = protocolVersion
	cfg.DelegateProtocolMin = protocolVersion
	cfg.Logger = stdlog.New(log.NewStdlibAdapter(logger), "", stdlog.Lshortfile)

	cfg.TCPTimeout = 10 * time.Second
	cfg.BindAddr = host
	cfg.BindPort = port
	cfg.AdvertisePort = port

	if !verbose {
		cfg.LogOutput = ioutil.Discard
	}

	cfg.SecretKey = secret
	cfg.Name = name

	cfg.Events = d
	cfg.Delegate = d

	return memberlist.Create(cfg)
}
