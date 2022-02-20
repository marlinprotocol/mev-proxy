package main

import (
	"flag"
	"fmt"
	"sync"
	"time"
)

func main() {
	listenAddrPtr := flag.String("listenAddr", "127.0.0.1:18545", "listen address")
	rpcAddrPtr := flag.String("rpcAddr", "127.0.0.1:8545", "rpc address")
	subgraphPathPtr := flag.String("subgraphPath", "/marlinprotocol/mev-bor", "subgraph path")
	bundleDispatchChanSize := flag.Int("bundleChan", 1000, "bundle dispatch channel size")
	epochTime := flag.Int64("epochTime", 5, "milliseconds to delay bundles for epoch calculations")
	bundlesPerEpoch := flag.Uint("bundlesPerEpoch", 2, "bundles to allow per epoch to validator")
	maxBundleRetries := flag.Uint("maxBundleRetries", 3, "number of epochs before a bundle drops due to low bgp")

	flag.Parse()

	fmt.Printf("Starting gateway with listenAddr: %s, rpcAddr: %s\n", *listenAddrPtr, *rpcAddrPtr)

	server := &Proxy{
		RpcAddr:            *rpcAddrPtr,
		Whitelist:          nil,
		SubgraphPath:       *subgraphPathPtr,
		BundleDispatchLock: sync.Mutex{},
		BundleDispatch:     make(chan BundleDispatchItem, *bundleDispatchChanSize),
		EpochTime:          time.Duration(*epochTime) * time.Millisecond,
		BundlesPerEpoch:    *bundlesPerEpoch,
		MaxBundleRetries:   *maxBundleRetries,
	}
	// Start the epoch loop
	go server.epochLoop()
	server.ListenAndServe(*listenAddrPtr)
}
