package network

import (
	"context"
	"log"
	"sync"
	"time"

	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/multiformats/go-multiaddr"
	"github.com/sirupsen/logrus"

	"github.com/masa-finance/masa-oracle/pkg/pubsub"
)

const (
	maxRetries  = 3
	retryDelay  = time.Second * 5
	PeerAdded   = "PeerAdded"
	PeerRemoved = "PeerRemoved"
)

type dbValidator struct{}

func (dbValidator) Validate(_ string, _ []byte) error        { return nil }
func (dbValidator) Select(_ string, _ [][]byte) (int, error) { return 0, nil }

func WithDht(ctx context.Context, host host.Host, bootstrapNodes []multiaddr.Multiaddr,
	protocolId, prefix protocol.ID, peerChan chan PeerEvent, isStaked bool) (*dht.IpfsDHT, error) {
	options := make([]dht.Option, 0)
	options = append(options, dht.Mode(dht.ModeAutoServer))
	options = append(options, dht.ProtocolPrefix(prefix))
	options = append(options, dht.NamespacedValidator("db", dbValidator{}))

	kademliaDHT, err := dht.New(ctx, host, options...)
	if err != nil {
		return nil, err
	}
	go monitorRoutingTable(ctx, kademliaDHT, time.Minute)

	kademliaDHT.RoutingTable().PeerAdded = func(p peer.ID) {
		logrus.Infof("Peer added to DHT: %s", p.String())

		pe := PeerEvent{
			AddrInfo: peer.AddrInfo{ID: p},
			Action:   PeerAdded,
			Source:   "kdht",
		}
		peerChan <- pe
	}

	kademliaDHT.RoutingTable().PeerRemoved = func(p peer.ID) {
		logrus.Infof("Peer removed from DHT: %s", p)
		pe := PeerEvent{
			AddrInfo: peer.AddrInfo{ID: p},
			Action:   PeerRemoved,
			Source:   "kdht",
		}
		peerChan <- pe
	}

	if err = kademliaDHT.Bootstrap(ctx); err != nil {
		return nil, err
	}

	var wg sync.WaitGroup
	peerConnectionCount := 0

	for _, peerAddr := range bootstrapNodes {
		peerInfo, err := peer.AddrInfoFromP2pAddr(peerAddr)
		if err != nil {
			logrus.Errorf("kdht: %s", err.Error())
		}
		if peerInfo.ID == host.ID() {
			logrus.Info("DHT Skipping connect to self")
			continue
		}
		// Add the bootstrap node to the DHT
		added, err := kademliaDHT.RoutingTable().TryAddPeer(peerInfo.ID, true, false)
		if err != nil {
			logrus.Warningf("Failed to add bootstrap peer %s to DHT: %v", peerInfo.ID, err)
		} else if !added {
			logrus.Warningf("Bootstrap peer %s was not added to DHT", peerInfo.ID)
		} else {
			logrus.Infof("Successfully added bootstrap peer %s to DHT", peerInfo.ID)
		}

		wg.Add(1)
		counter := 0
		go func() {
			ctxWithTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel() // Cancel the context when done to release resources

			defer wg.Done()
			if err := host.Connect(ctxWithTimeout, *peerInfo); err != nil {
				logrus.Errorf("Failed to connect to bootstrap peer %s: %v", peerInfo.ID, err)
				counter++
				if counter >= maxRetries {
					return
				}
				time.Sleep(retryDelay)
			} else {
				logrus.Info("Connection established with node:", *peerInfo)
				stream, err := host.NewStream(ctxWithTimeout, peerInfo.ID, protocolId)
				if err != nil {
					logrus.Error("Error opening stream:", err)
					return
				}
				peerConnectionCount++
				defer func(stream network.Stream) {
					err := stream.Close()
					if err != nil {
						logrus.Error("Error closing stream:", err)
					}
				}(stream) // Close the stream when done
				_, err = stream.Write(pubsub.GetSelfNodeDataJson(host, isStaked))
				if err != nil {
					logrus.Error("Error writing to stream:", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if len(bootstrapNodes) > 0 && peerConnectionCount == 0 {
		log.Println("Unable to connect to a boot node at this time. Waiting...")
	}
	return kademliaDHT, nil
}

func monitorRoutingTable(ctx context.Context, dht *dht.IpfsDHT, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// This block will be executed every 'interval' duration
			routingTable := dht.RoutingTable()
			// Log the size of the routing table
			logrus.Infof("Routing table size: %d", routingTable.Size())
			// Log the peer IDs in the routing table
			for _, p := range routingTable.ListPeers() {
				logrus.Debugf("Peer in routing table: %s", p.String())
			}
		case <-ctx.Done():
			// If the context is cancelled, stop the goroutine
			return
		}
	}
}
