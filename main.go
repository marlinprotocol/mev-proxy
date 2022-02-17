package main

import (
	"flag"
	"fmt"
)

func main() {
	listenAddrPtr := flag.String("listenAddr", "127.0.0.1:18545", "listen address")
	rpcAddrPtr := flag.String("rpcAddr", "127.0.0.1:8545", "rpc address")
	subgraphPathPtr := flag.String("subgraphPath", "/marlinprotocol/mev-bor", "subgraph path")
	gasLimitPerBundle := flag.Int64("gasLimitPerBundle", 2500000, "gas limit per bundle")
	txLimitPerBundle := flag.Int64("txLimitPerBundle", 5, "tx limit per bundle")

	flag.Parse()

	fmt.Printf("Starting gateway with listenAddr: %s, rpcAddr: %s\n", *listenAddrPtr, *rpcAddrPtr)

	g := &Proxy{*rpcAddrPtr, nil, *subgraphPathPtr, uint64(*gasLimitPerBundle), int(*txLimitPerBundle)}
	g.ListenAndServe(*listenAddrPtr)
}
