package main

import (
	"flag"
	"fmt"
)

func main() {
	listenAddrPtr := flag.String("listenAddr", "127.0.0.1:18545", "listen address")
	rpcAddrPtr := flag.String("rpcAddr", "127.0.0.1:8545", "rpc address")

	flag.Parse()

	fmt.Println("Starting gateway with listenAddr: {}, rpcAddr: {}", listenAddrPtr, rpcAddrPtr)

	g := &Proxy{*rpcAddrPtr, nil}
	g.ListenAndServe(*listenAddrPtr)
}

