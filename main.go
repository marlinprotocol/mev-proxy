package main

import (
	"flag"
	"fmt"
	"time"
)

func main() {
	listenAddrPtr := flag.String("listenAddr", "127.0.0.1:18545", "listen address")
	rpcAddrPtr := flag.String("rpcAddr", "127.0.0.1:8545", "rpc address")
	subgraphPathPtr := flag.String("subgraphPath", "/marlinprotocol/mev-bor", "subgraph path")
	bundleDispatchChanSize := flag.Int("bundleChan", 1000, "bundle dispatch channel size")
	bundleDelayStep := flag.Int64("delayStep", 5, "milliseconds to delay bundles in case of spam")

	flag.Parse()

	fmt.Printf("Starting gateway with listenAddr: %s, rpcAddr: %s\n", *listenAddrPtr, *rpcAddrPtr)

	g := &Proxy{*rpcAddrPtr, nil, *subgraphPathPtr, make(chan *RpcReq, *bundleDispatchChanSize), time.Duration(*bundleDelayStep) * time.Millisecond}
	g.ListenAndServe(*listenAddrPtr)
}
