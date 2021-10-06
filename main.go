package main

import (
	"flag"
	"fmt"
)

func main() {
	listenAddrPtr := flag.String("listenAddr", "127.0.0.1:18545", "listen address")
	rpcAddrPtr := flag.String("rpcAddr", "127.0.0.1:8545", "rpc address")
	subgraphPathPtr := flag.String("subgraphPath", "/marlinprotocol/mev-bor", "subgraph path")

	flag.Parse()

	fmt.Printf("Starting gateway with listenAddr: %s, rpcAddr: %s\n", *listenAddrPtr, *rpcAddrPtr)

	g := &Proxy{*rpcAddrPtr, nil, *subgraphPathPtr}
	g.ListenAndServe(*listenAddrPtr)
}
