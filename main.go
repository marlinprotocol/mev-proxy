package main

import (
	"flag"
	"fmt"
	"time"

	"golang.org/x/time/rate"
)

func main() {
	listenAddrPtr := flag.String("listenAddr", "127.0.0.1:18545", "listen address")
	rpcAddrPtr := flag.String("rpcAddr", "127.0.0.1:8545", "rpc address")
	subgraphPathPtr := flag.String("subgraphPath", "/marlinprotocol/mev-bor", "subgraph path")
	limiterTimePtr := flag.Int64("limiterTime", 5, "allow one bundle every <X> ms")
	limiterBurstPtr := flag.Int("limiterBurst", 3, "max burst for rate limiter")

	flag.Parse()

	fmt.Printf("Starting gateway with listenAddr: %s, rpcAddr: %s\n", *listenAddrPtr, *rpcAddrPtr)

	limit := rate.Every(time.Millisecond * time.Duration(*limiterTimePtr))
	limiter := rate.NewLimiter(limit, *limiterBurstPtr)
	g := &Proxy{*rpcAddrPtr, nil, *subgraphPathPtr, limiter}
	g.ListenAndServe(*listenAddrPtr)
}
