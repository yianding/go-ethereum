// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// Contains all the wrappers from the node package to support client side node
// management on mobile platforms.

package geth

import (
	"encoding/json"
	"fmt"
	"math/big"
	"path/filepath"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/ethash"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/eth"
	"github.com/ethereum/go-ethereum/eth/downloader"
	"github.com/ethereum/go-ethereum/eth/ethconfig"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/internal/debug"
	"github.com/ethereum/go-ethereum/miner"

	//"github.com/ethereum/go-ethereum/eth"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/node"
	"github.com/ethereum/go-ethereum/p2p"
	"github.com/ethereum/go-ethereum/p2p/nat"
	"github.com/ethereum/go-ethereum/params"
)

// NodeConfig represents the collection of configuration values to fine tune the Geth
// node embedded into a mobile process. The available values are a subset of the
// entire API provided by go-ethereum to reduce the maintenance surface and dev
// complexity.
type NodeConfig struct {
	// Bootstrap nodes used to establish connectivity with the rest of the network.
	BootstrapNodes *Enodes

	// MaxPeers is the maximum number of peers that can be connected. If this is
	// set to zero, then only the configured static and trusted peers can connect.
	MaxPeers int

	// EthereumEnabled specifies whether the node should run the Ethereum protocol.
	EthereumEnabled bool

	// EthereumNetworkID is the network identifier used by the Ethereum protocol to
	// decide if remote peers should be accepted or not.
	EthereumNetworkID int64 // uint64 in truth, but Java can't handle that...

	// EthereumGenesis is the genesis JSON to use to seed the blockchain with. An
	// empty genesis state is equivalent to using the mainnet's state.
	EthereumGenesis string

	// EthereumDatabaseCache is the system memory in MB to allocate for database caching.
	// A minimum of 16MB is always reserved.
	EthereumDatabaseCache int

	// EthereumNetStats is a netstats connection string to use to report various
	// chain, transaction and node stats to a monitoring server.
	//
	// It has the form "nodename:secret@host:port"
	EthereumNetStats string

	// Listening address of pprof server.
	PprofAddress string
}

// defaultNodeConfig contains the default node configuration values to use if all
// or some fields are missing from the user's specified list.
var defaultNodeConfig = &NodeConfig{
	BootstrapNodes:        FoundationBootnodes(),
	MaxPeers:              25,
	EthereumEnabled:       true,
	EthereumNetworkID:     1000,
	EthereumDatabaseCache: 16,
}

// NewNodeConfig creates a new node option set, initialized to the default values.
func NewNodeConfig() *NodeConfig {
	config := *defaultNodeConfig
	return &config
}

// AddBootstrapNode adds an additional bootstrap node to the node config.
func (conf *NodeConfig) AddBootstrapNode(node *Enode) {
	conf.BootstrapNodes.Append(node)
}

// EncodeJSON encodes a NodeConfig into a JSON data dump.
func (conf *NodeConfig) EncodeJSON() (string, error) {
	data, err := json.Marshal(conf)
	return string(data), err
}

// String returns a printable representation of the node config.
func (conf *NodeConfig) String() string {
	return encodeOrError(conf)
}

// Node represents a Geth Ethereum node instance.
type Node struct {
	node       *node.Node
	ethBackend *eth.Ethereum
}

// NewNode creates and configures a new Geth node.
func NewNode(datadir string, config *NodeConfig, minebaseAddress string, threads int) (stack *Node, _ error) {
	// If no or partial configurations were specified, use defaults
	var ethBackendPint *eth.Ethereum
	if config == nil {
		config = NewNodeConfig()
	}
	if config.MaxPeers == 0 {
		config.MaxPeers = defaultNodeConfig.MaxPeers
	}
	if config.BootstrapNodes == nil || config.BootstrapNodes.Size() == 0 {
		config.BootstrapNodes = defaultNodeConfig.BootstrapNodes
		log.Info("Have not BootstrapNodes ")
	}
	if config.PprofAddress != "" {
		debug.StartPProf(config.PprofAddress, true)
	}

	// Create the empty networking stack
	nodeConf := &node.Config{

		Name:        clientIdentifier,
		Version:     params.VersionWithMeta,
		DataDir:     datadir,
		HTTPHost:    "0.0.0.0",
		HTTPModules: []string{"net", "web3", "eth", "personal"},
		HTTPPort:    8999,
		HTTPCors:    []string{"*"},
		WSHost:      "0.0.0.0",
		WSPort:      8998,
		WSOrigins:   []string{"*"},

		KeyStoreDir: filepath.Join(datadir, "keystore"), // Mobile should never use internal keystores!

		P2P: p2p.Config{
			NoDiscovery:      false,
			DiscoveryV5:      true,
			BootstrapNodesV5: config.BootstrapNodes.nodes,
			StaticNodes:      config.BootstrapNodes.nodes,
			TrustedNodes:     config.BootstrapNodes.nodes,
			ListenAddr:       ":43448",
			NAT:              nat.Any(),
			MaxPeers:         config.MaxPeers,
		},
	}
	log.Info("Start Node Andy " + config.BootstrapNodes.nodes[0].String())

	rawStack, err := node.New(nodeConf)
	if err != nil {
		return nil, err
	}

	debug.Memsize.Add("node", rawStack)

	var genesis *core.Genesis
	if config.EthereumGenesis != "" {
		// Parse the user supplied genesis spec if not mainnet
		genesis = new(core.Genesis)
		if err := json.Unmarshal([]byte(config.EthereumGenesis), genesis); err != nil {
			return nil, fmt.Errorf("invalid genesis spec: %v", err)
		}
		// If we have the Ropsten testnet, hard code the chain configs too
		if config.EthereumGenesis == RopstenGenesis() {
			genesis.Config = params.RopstenChainConfig
			if config.EthereumNetworkID == 1 {
				config.EthereumNetworkID = 3
			}
		}
		// If we have the Sepolia testnet, hard code the chain configs too
		if config.EthereumGenesis == SepoliaGenesis() {
			genesis.Config = params.SepoliaChainConfig
			if config.EthereumNetworkID == 1 {
				config.EthereumNetworkID = 11155111
			}
		}
		// If we have the Rinkeby testnet, hard code the chain configs too
		if config.EthereumGenesis == RinkebyGenesis() {
			genesis.Config = params.RinkebyChainConfig
			if config.EthereumNetworkID == 1 {
				config.EthereumNetworkID = 4
			}
		}
		// If we have the Goerli testnet, hard code the chain configs too
		if config.EthereumGenesis == GoerliGenesis() {
			genesis.Config = params.GoerliChainConfig
			if config.EthereumNetworkID == 1 {
				config.EthereumNetworkID = 5
			}
		}
	}
	//	Register the Ethereum protocol if requested

	if config.EthereumEnabled {
		ethConf := ethconfig.Defaults
		ethConf.Genesis = genesis

		ethConf.SyncMode = downloader.SnapSync
		ethConf.NetworkId = uint64(config.EthereumNetworkID)
		ethConf.DatabaseCache = config.EthereumDatabaseCache
		ethConf.Ethash = ethash.Config{
			DatasetDir: filepath.Join(datadir, "dag"),
		}
		if len(minebaseAddress) > 30 {
			ethConf.Miner = miner.Config{
				//Etherbase: common.Address{1},
				Etherbase: common.HexToAddress(minebaseAddress),
				GasCeil:   genesis.GasLimit * 11 / 10,
				GasPrice:  big.NewInt(1),
				Recommit:  time.Second,
			}
		}

		ethBackend, err := eth.New(rawStack, &ethConf)
		// if threads > 0 {
		// 	log.Warn("ANDY:START Mining %i", threads)
		// 	if err := ethBackend.StartMining(threads); err != nil {

		// 	}
		// }
		if err != nil {
			return nil, fmt.Errorf("ethereum init: %v", err)
		}
		ethBackendPint = ethBackend

	}

	return &Node{rawStack, ethBackendPint}, nil
}

// Close terminates a running node along with all it's services, tearing internal state
// down. It is not possible to restart a closed node.
func (n *Node) Close() error {

	errr := n.node.Close()
	return errr
}

func (n *Node) StartMining(i int) error {
	// defer func() {
	// 	if err := recover(); err != nil {
	// 		fmt.Println("ANDY: START MINING error:", err)
	// 	}
	// }()
	log.Warn("ANDY:START MINING %i", i)

	err := n.ethBackend.StartMining(i)
	log.Warn("ANDY:STARTED MINING")
	return err

}

// Start creates a live P2P node and starts running it.
func (n *Node) Start() error {
	// TODO: recreate the node so it can be started multiple times
	return n.node.Start()
}

// GetEthereumClient retrieves a client to access the Ethereum subsystem.
func (n *Node) GetEthereumClient() (client *EthereumClient, _ error) {
	rpc, err := n.node.Attach()
	if err != nil {
		return nil, err
	}
	return &EthereumClient{ethclient.NewClient(rpc)}, nil
}
func (n *Node) GetEthereumHttp() (client string) {
	return n.node.HTTPEndpoint()
}

// GetNodeInfo gathers and returns a collection of metadata known about the host.
func (n *Node) GetNodeInfo() *NodeInfo {
	return &NodeInfo{n.node.Server().NodeInfo()}
}

// GetPeersInfo returns an array of metadata objects describing connected peers.
func (n *Node) GetPeersInfo() *PeerInfos {
	return &PeerInfos{n.node.Server().PeersInfo()}
}
