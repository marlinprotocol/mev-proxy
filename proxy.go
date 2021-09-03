package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/ethereum/go-ethereum/crypto/secp256k1"
	"golang.org/x/crypto/sha3"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync/atomic"
	"time"
	"unsafe"
)

type Proxy struct {
	RpcAddr string
	// We will atomically update this to avoid explicit locks
	// In modern systems, should avoid _any_ locks
	Whitelist unsafe.Pointer
}

type RpcReq struct {
	Jsonrpc string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Id      interface{}     `json:"id"`
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

func fetchWhitelist() ([]string, error) {
	graphURL := "https://api.thegraph.com/subgraphs/name/marlinprotocol/mev-bor"
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
		resp = p.handleEthSendBundle(req)
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

func (p *Proxy) ListenAndServe(addr string) {
	// spawn whitelist routine
	atomic.StorePointer(&p.Whitelist, unsafe.Pointer(new([]string)))
	go func() {
		ticker := time.NewTicker(6 * time.Second)
		for {
			keys, err := fetchWhitelist()
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
