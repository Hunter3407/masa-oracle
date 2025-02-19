package masa

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"strings"
	"time"

	"github.com/libp2p/go-libp2p"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/protocol"
	rcmgr "github.com/libp2p/go-libp2p/p2p/host/resource-manager"
	"github.com/libp2p/go-libp2p/p2p/muxer/yamux"
	"github.com/libp2p/go-libp2p/p2p/security/noise"
	libp2ptls "github.com/libp2p/go-libp2p/p2p/security/tls"
	quic "github.com/libp2p/go-libp2p/p2p/transport/quic"
	"github.com/libp2p/go-libp2p/p2p/transport/tcp"
	"github.com/multiformats/go-multiaddr"
	"github.com/sirupsen/logrus"

	"github.com/masa-finance/masa-oracle/pkg/ad"
	"github.com/masa-finance/masa-oracle/pkg/config"
	"github.com/masa-finance/masa-oracle/pkg/masacrypto"
	myNetwork "github.com/masa-finance/masa-oracle/pkg/network"
	"github.com/masa-finance/masa-oracle/pkg/nodestatus"
	pubsub2 "github.com/masa-finance/masa-oracle/pkg/pubsub"
)

type OracleNode struct {
	Host                           host.Host
	PrivKey                        *ecdsa.PrivateKey
	Protocol                       protocol.ID
	priorityAddrs                  multiaddr.Multiaddr
	multiAddrs                     []multiaddr.Multiaddr
	DHT                            *dht.IpfsDHT
	Context                        context.Context
	PeerChan                       chan myNetwork.PeerEvent
	NodeTracker                    *pubsub2.NodeEventTracker
	PubSubManager                  *pubsub2.Manager
	Signature                      string
	IsStaked                       bool
	StartTime                      time.Time
	AdSubscriptionHandler          *ad.SubscriptionHandler
	NodeStatusSubscriptionsHandler *nodestatus.SubscriptionHandler
}

func (node *OracleNode) GetMultiAddrs() multiaddr.Multiaddr {
	if node.priorityAddrs == nil {
		pAddr := myNetwork.GetPriorityAddress(node.multiAddrs)
		node.priorityAddrs = pAddr
	}
	return node.priorityAddrs
}

func NewOracleNode(ctx context.Context, isStaked bool) (*OracleNode, error) {
	// Start with the default scaling limits.
	cfg := config.GetInstance()
	scalingLimits := rcmgr.DefaultLimits
	concreteLimits := scalingLimits.AutoScale()
	limiter := rcmgr.NewFixedLimiter(concreteLimits)

	resourceManager, err := rcmgr.NewResourceManager(limiter)
	if err != nil {
		return nil, err
	}

	var addrStr []string
	libp2pOptions := []libp2p.Option{
		libp2p.Identity(masacrypto.KeyManagerInstance().Libp2pPrivKey),
		libp2p.ResourceManager(resourceManager),
		libp2p.Ping(false), // disable built-in ping
		libp2p.EnableNATService(),
		libp2p.NATPortMap(),
		libp2p.EnableRelay(), // Enable Circuit Relay v2 with hop
	}

	securityOptions := []libp2p.Option{
		libp2p.Security(noise.ID, noise.New),
	}
	if cfg.UDP {
		addrStr = append(addrStr, fmt.Sprintf("/ip4/0.0.0.0/udp/%d/quic-v1", cfg.PortNbr))
		libp2pOptions = append(libp2pOptions, libp2p.Transport(quic.NewTransport))
	}
	if cfg.TCP {
		securityOptions = append(securityOptions, libp2p.Security(libp2ptls.ID, libp2ptls.New))
		addrStr = append(addrStr, fmt.Sprintf("/ip4/0.0.0.0/tcp/%d", cfg.PortNbr))
		libp2pOptions = append(libp2pOptions, libp2p.Transport(tcp.NewTCPTransport))
		libp2pOptions = append(libp2pOptions, libp2p.Muxer("/yamux/1.0.0", yamux.DefaultTransport))
	}
	libp2pOptions = append(libp2pOptions, libp2p.ChainOptions(securityOptions...))
	libp2pOptions = append(libp2pOptions, libp2p.ListenAddrStrings(addrStr...))

	hst, err := libp2p.New(libp2pOptions...)
	if err != nil {
		return nil, err
	}

	subscriptionManager, err := pubsub2.NewPubSubManager(ctx, hst)
	if err != nil {
		return nil, err
	}

	return &OracleNode{
		Host:          hst,
		PrivKey:       masacrypto.KeyManagerInstance().EcdsaPrivKey,
		Protocol:      config.ProtocolWithVersion(config.OracleProtocol),
		multiAddrs:    myNetwork.GetMultiAddressesForHostQuiet(hst),
		Context:       ctx,
		PeerChan:      make(chan myNetwork.PeerEvent),
		NodeTracker:   pubsub2.NewNodeEventTracker(config.Version, cfg.Environment),
		PubSubManager: subscriptionManager,
		IsStaked:      isStaked,
	}, nil
}

func (node *OracleNode) Start() (err error) {
	logrus.Infof("Starting node with ID: %s", node.GetMultiAddrs().String())

	bootNodeAddrs, err := myNetwork.GetBootNodesMultiAddress(config.GetInstance().Bootnodes)
	if err != nil {
		return err
	}

	node.Host.SetStreamHandler(node.Protocol, node.handleStream)
	node.Host.SetStreamHandler(config.ProtocolWithVersion(config.NodeDataSyncProtocol), node.ReceiveNodeData)
	// node.Host.SetStreamHandler(config.ProtocolWithVersion(config.NodeStatusTopic), node.ReceiveNodeData)
	if node.IsStaked {
		node.Host.SetStreamHandler(config.ProtocolWithVersion(config.NodeGossipTopic), node.GossipNodeData)
	}
	node.Host.Network().Notify(node.NodeTracker)

	go node.ListenToNodeTracker()
	go node.handleDiscoveredPeers()

	node.DHT, err = myNetwork.WithDht(node.Context, node.Host, bootNodeAddrs, node.Protocol, config.MasaPrefix, node.PeerChan, node.IsStaked)
	if err != nil {
		return err
	}
	err = myNetwork.WithMDNS(node.Host, config.Rendezvous, node.PeerChan)
	if err != nil {
		return err
	}

	go myNetwork.Discover(node.Context, node.Host, node.DHT, node.Protocol)
	// if this is the original boot node then add it to the node tracker
	if config.GetInstance().HasBootnodes() {
		nodeData := node.NodeTracker.GetNodeData(node.Host.ID().String())
		if nodeData == nil {
			publicKeyHex := masacrypto.KeyManagerInstance().EthAddress
			nodeData = pubsub2.NewNodeData(node.GetMultiAddrs(), node.Host.ID(), publicKeyHex, pubsub2.ActivityJoined)
			nodeData.IsStaked = node.IsStaked
			nodeData.SelfIdentified = true
		}
		nodeData.Joined()
		node.NodeTracker.HandleNodeData(*nodeData)
	}
	// call SubscribeToTopics on startup
	if err := SubscribeToTopics(node); err != nil {
		return err
	}
	node.StartTime = time.Now()

	return nil
}

func (node *OracleNode) handleDiscoveredPeers() {
	for {
		select {
		case peer := <-node.PeerChan: // will block until we discover a peer
			logrus.Debugf("Peer Event for: %s, Action: %s", peer.AddrInfo.ID.String(), peer.Action)
			// If the peer is a new peer, connect to it
			if peer.Action == myNetwork.PeerAdded {
				if err := node.Host.Connect(node.Context, peer.AddrInfo); err != nil {
					logrus.Errorf("Connection failed for peer: %s %v", peer.AddrInfo.ID.String(), err)
					// close the connection
					err := node.Host.Network().ClosePeer(peer.AddrInfo.ID)
					if err != nil {
						logrus.Error(err)
					}
					continue
				}
			}
		case <-node.Context.Done():
			return
		}
	}
}

func (node *OracleNode) handleStream(stream network.Stream) {
	remotePeer, nodeData, err := node.handleStreamData(stream)
	if err != nil {
		if strings.HasPrefix(err.Error(), "un-staked") {
			// just ignore the error
			return
		}
		logrus.Errorf("Failed to read stream: %v", err)
		return
	}
	if remotePeer.String() != nodeData.PeerId.String() {
		logrus.Warnf("Received data from unexpected peer %s", remotePeer)
		return
	}
	multiAddr := stream.Conn().RemoteMultiaddr()
	newNodeData := pubsub2.NewNodeData(multiAddr, remotePeer, nodeData.EthAddress, pubsub2.ActivityJoined)
	newNodeData.IsStaked = nodeData.IsStaked
	err = node.NodeTracker.AddOrUpdateNodeData(newNodeData, false)
	if err != nil {
		logrus.Error(err)
		return
	}
	logrus.Info("handleStream -> Received data from:", remotePeer.String())
}

func (node *OracleNode) IsPublisher() bool {
	// Node is a publisher if it has a non-empty signature
	return node.Signature != ""
}

func (node *OracleNode) Version() string {
	return config.Version
}

func (node *OracleNode) LogActiveTopics() {
	topicNames := node.PubSubManager.GetTopicNames()
	if len(topicNames) > 0 {
		logrus.Infof("Active topics: %v", topicNames)
	} else {
		logrus.Info("No active topics.")
	}
}
