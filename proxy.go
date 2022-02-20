package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto/secp256k1"
	"golang.org/x/crypto/sha3"
)

type Proxy struct {
	RpcAddr string
	// We will atomically update this to avoid explicit locks
	// In modern systems, should avoid _any_ locks
	Whitelist          unsafe.Pointer
	SubgraphPath       string
	BundleDispatchLock sync.Mutex
	BundleDispatch     chan BundleDispatchItem
	EpochTime          time.Duration
	BundlesPerEpoch    uint
	MaxBundleRetries   uint
}

type SendBundleArgs struct {
	Txs               []hexutil.Bytes        `json:"txs"`
	BlockNumber       string                 `json:"blockNumber"`
	MinTimestamp      json.RawMessage        `json:"minTimestamp,omitempty"`
	MaxTimestamp      json.RawMessage        `json:"maxTimestamp,omitempty"`
	RevertingTxHashes json.RawMessage        `json:"revertingTxHashes,omitempty"`
	ExtraInfo         map[string]interface{} `json:"extraInfo,omitempty"`
}

type RpcReq struct {
	Jsonrpc string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Id      interface{}     `json:"id"`
}

type BundleDispatchItem struct {
	data           *RpcReq
	bundleGasPrice *big.Int
	retry          uint
}

type BundleDispatchVec []BundleDispatchItem

func (p BundleDispatchVec) Len() int {
	return len(p)
}

func (p BundleDispatchVec) Less(i, j int) bool {
	cmp := p[i].bundleGasPrice.Cmp(p[j].bundleGasPrice) // Is -1 for Less, 0 for Eq, 1 for Greater
	if cmp == -1 {
		return true
	}
	return false
}

func (p BundleDispatchVec) Swap(i, j int) {
	tempBundle := p[i]
	p[i] = p[j]
	p[j] = tempBundle
}

type RpcErr struct {
	Code    int64       `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type RpcResp struct {
	Jsonrpc string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RpcErr     `json:"error,omitempty"`
	Id      interface{} `json:"id"`
}

func makeRpcCall(req *RpcReq, rpcAddr string) *RpcResp {
	reqBytes, _ := json.Marshal(req)
	r, err := http.Post(rpcAddr, "application/json", bytes.NewReader(reqBytes))

	if err != nil {
		return &RpcResp{
			"2.0",
			nil,
			&RpcErr{
				-32603,
				"Upstream unreachable",
				nil,
			},
			req.Id,
		}
	}

	// WARN: Should ideally use Content-Length here but the RPC server does not send it
	bodyLength := 1000000
	if r.Header.Get("Content-Type") != "application/json" ||
		bodyLength <= 0 {
		return &RpcResp{
			"2.0",
			nil,
			&RpcErr{
				-32603,
				"Upstream response error",
				nil,
			},
			req.Id,
		}
	}

	decoder := json.NewDecoder(io.LimitReader(r.Body, int64(bodyLength)))
	var resp *RpcResp = &RpcResp{}
	err = decoder.Decode(resp)
	if err != nil || resp.Jsonrpc != "2.0" {
		return &RpcResp{
			"2.0",
			nil,
			&RpcErr{
				-32603,
				"Upstream response error",
				nil,
			},
			req.Id,
		}
	}

	return resp
}

type WhitelistResp struct {
	Data struct {
		Keystores []struct {
			Key string `json:"key"`
		} `json:"keystores"`
	} `json:"data"`
}

func (p *Proxy) fetchWhitelist() ([]string, error) {
	graphURL := "https://api.thegraph.com/subgraphs/name" + p.SubgraphPath
	reqBytes := []byte(`{"query": "query { keystores { key } }"}`)
	// fmt.Println(string(reqBytes))
	r, err := http.Post(graphURL, "application/json", bytes.NewReader(reqBytes))

	if err != nil {
		return nil, err
	}

	// WARN: Should ideally use Content-Length here but the RPC server does not send it
	bodyLength := 1000000
	// fmt.Println(r)
	if r.Header.Get("content-type") != "application/json" ||
		bodyLength <= 0 {
		return nil, fmt.Errorf("Response content type mismatch")
	}

	decoder := json.NewDecoder(io.LimitReader(r.Body, int64(bodyLength)))
	resp := &WhitelistResp{}
	err = decoder.Decode(resp)
	if err != nil {
		return nil, fmt.Errorf("Response decode error")
	}

	// Are we List.map yet instead of this abomination?
	keys := make([]string, len(resp.Data.Keystores))
	for idx, keyResp := range resp.Data.Keystores {
		keys[idx] = keyResp.Key
	}
	// fmt.Println(keys)
	return keys, nil
}

func (p *Proxy) handleEthSendBundle(req *RpcReq) *RpcResp {
	// bundle RPC APIs now moved to the mev namespace
	req.Method = "mev_sendBundle"
	return makeRpcCall(req, p.RpcAddr)
}

func (p *Proxy) handleRpc(w http.ResponseWriter, r *http.Request) {
	// Verify method and path
	if r.Method != "POST" || r.URL.Path != "/" {
		w.WriteHeader(404)
		return
	}

	bodyLength, err := strconv.Atoi(r.Header.Get("Content-Length"))
	if r.Header.Get("Content-Type") != "application/json" ||
		err != nil ||
		bodyLength == 0 {
		w.WriteHeader(400)
		w.Write([]byte("Invalid content type"))
		return
	}

	// Verify request format and version
	decoder := json.NewDecoder(io.LimitReader(r.Body, int64(bodyLength)))
	var req *RpcReq = &RpcReq{}
	err = decoder.Decode(req)
	if err != nil || req.Jsonrpc != "2.0" {
		w.WriteHeader(400)
		w.Write([]byte("Request decode error"))
		return
	}

	// Retrieve signature key
	relaySigStr := r.Header.Get("X-Marlin-Signature")
	// fmt.Println(relaySigStr)
	relaySigBytes, err := hex.DecodeString(relaySigStr[2:])
	if err != nil {
		w.WriteHeader(400)
		w.Write([]byte("Signature decode error"))
		return
	}

	hasher := sha3.NewLegacyKeccak256()
	hasher.Write([]byte("\x19Bor Signed MEV TxBundle:\n"))
	hasher.Write(req.Params)
	msgHash := hasher.Sum(nil)

	pubkey, err := secp256k1.RecoverPubkey(msgHash, relaySigBytes)
	if err != nil {
		w.WriteHeader(400)
		w.Write([]byte("Signature recovery error"))
		return
	}

	// Transform into address
	hasher.Reset()
	hasher.Write(pubkey[1:])
	addrBytes := hasher.Sum(nil)[12:]
	addr := fmt.Sprintf("0x%x", addrBytes)
	fmt.Println("Bundle received from ", addr)

	// Retrieve whitelist
	whitelistPtr := atomic.LoadPointer(&p.Whitelist)
	whitelist := (*[]string)(whitelistPtr)

	// fmt.Println("Whitelist: ", *whitelist)

	// Verify whitelisted
	idx := sort.SearchStrings(*whitelist, addr)
	if (*whitelist)[idx] != addr {
		w.WriteHeader(400)
		return
	}

	var resp *RpcResp
	if req.Method == "eth_sendBundle" {
		p.BundleDispatchLock.Lock()
		defer p.BundleDispatchLock.Unlock()

		if len(p.BundleDispatch) == cap(p.BundleDispatch) {
			// Silent drop
			w.WriteHeader(400)
			return
		}

		var extraInfo map[string]interface{}
		err := json.Unmarshal(req.Params, &extraInfo)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		if bgp, ok := extraInfo["bundleGasPrice"]; ok {
			bgpBigInt, ok := new(big.Int).SetString(bgp.(string), 10)
			if !ok {
				w.WriteHeader(400)
				return
			}

			p.BundleDispatch <- BundleDispatchItem{req, bgpBigInt, 0}
			// Eager return
			resp = &RpcResp{
				Jsonrpc: req.Jsonrpc,
				Result:  "queued for proxy dispatch",
				Error:   nil,
				Id:      req.Id,
			}
		} else {
			w.WriteHeader(400)
			return
		}
	} else {
		resp = &RpcResp{
			"2.0",
			nil,
			&RpcErr{
				-32601,
				"Method not found",
				nil,
			},
			req.Id,
		}
	}

	respBytes, err := json.Marshal(resp)
	if err != nil {
		w.WriteHeader(500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(respBytes)))
	w.Write(respBytes)

	return
}

// Runs once every epoch.
func (p *Proxy) epochLoop() {
	for {
		nextEpoch := time.Now().Add(p.EpochTime)

		p.BundleDispatchLock.Lock()

		// Drain the dispatch channel
		lenBundleDispatch := len(p.BundleDispatch)
		bundles := make(BundleDispatchVec, lenBundleDispatch)
		for i := 0; i < lenBundleDispatch; i++ {
			bundles[i] = <-p.BundleDispatch
		}

		// Sort bundles
		sort.Sort(sort.Reverse(bundles))

		// Gather top bundles
		selectedBundles := []*RpcReq{}
		for i := uint(0); i < p.BundlesPerEpoch; i++ {
			if len(bundles) == 0 {
				break
			}
			selectedBundles = append(selectedBundles, bundles[0].data)
			bundles = bundles[1:]
		}

		// Reinsert eligible bundles into channel
		for _, b := range bundles {
			if b.retry >= p.MaxBundleRetries {
				// Ditch this bundle
				continue
			}
			p.BundleDispatch <- BundleDispatchItem{b.data, b.bundleGasPrice, b.retry + 1}
		}
		// Eager unlock so we don't keep chan locked
		// while we do RPC requests
		p.BundleDispatchLock.Unlock()

		p.sendBundlesToValidator(selectedBundles)

		// Should be less than EpochTime as processing time has been deducted
		time.Sleep(nextEpoch.Sub(time.Now()))
	}
}

// Single shot parallel delivery
func (p *Proxy) sendBundlesToValidator(bundles []*RpcReq) {
	var wg sync.WaitGroup
	for _, bundle := range bundles {
		wg.Add(1)
		go func(bundle *RpcReq) {
			_ = p.handleEthSendBundle(bundle)
			wg.Done()
		}(bundle)
	}
	wg.Wait()
}

func (p *Proxy) ListenAndServe(addr string) {
	// spawn whitelist routine
	atomic.StorePointer(&p.Whitelist, unsafe.Pointer(new([]string)))
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		for {
			keys, err := p.fetchWhitelist()
			if err != nil {
				fmt.Println("whitelist fetch err", err)
				<-ticker.C
				continue
			}

			sort.Strings(keys)

			// fmt.Println(keys)

			// storing pointer to slice here
			atomic.StorePointer(&p.Whitelist, unsafe.Pointer(&keys))

			<-ticker.C
		}
	}()

	http.HandleFunc("/", p.handleRpc)

	log.Fatal(http.ListenAndServe(addr, nil))
}
