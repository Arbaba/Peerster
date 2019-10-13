package main

import (
	//	"Peerster/nodes"
	"Peerster/nodes"
	"Peerster/packets"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"protobuf"
	"strings"
)

func main() {
	uiport, gossipAddr, name, peers, simpleMode := parseCmd()
	//fmt.Printf("Port %s\nGossipAddr %s\nName %s\nPeers %s\nSimpleMode %t\n", *uiport, *gossipAddr, *name, *peers, *simpleMode)
	//parse IP append uiport
	gossiper := newGossiper(*gossipAddr, *name, *uiport, peers, *simpleMode)
	go listenClient(gossiper)
	listenGossip(gossiper)
}

func parseCmd() (*string, *string, *string, []string, *bool) {
	//Parse arguments
	uiport := flag.String("UIPort", "8080", "port for the UI client")
	gossipAddr := flag.String("gossipAddr", "127.0.0.1:5000", "ip:port for the gossiper")
	name := flag.String("name", "", "name of the gossiper")
	peers := flag.String("peers", "", "comma separated list of peers of the form ip:port")
	simpleMode := flag.Bool("simple", false, "run gossiper in simple broadcast mode")
	flag.Parse()
	peersList := []string{}
	if *peers != "" {
		peersList = strings.Split(*peers, ",")
	}
	return uiport, gossipAddr, name, peersList, simpleMode
}

/*
func check(err error, msg string) {
	if err != nil {
		log.Fatal(msg)
	}
}*/

func udpConnection(address string) (*net.UDPAddr, *net.UDPConn) {
	//TODO: Check for errors
	udpAddr, _ := net.ResolveUDPAddr("udp4", address)
	udpConn, _ := net.ListenUDP("udp4", udpAddr)
	return udpAddr, udpConn
}

func newGossiper(address, namee, uiport string, peers []string, simpleMode bool) *nodes.Gossiper {
	splitted := strings.Split(address, ":")
	ip := splitted[0]

	gossipAddr, gossipConn := udpConnection(address)
	clientAddr, clientConn := udpConnection(fmt.Sprintf("%s:%s", ip, uiport))

	return &nodes.Gossiper{
		GossipAddr:     gossipAddr,
		GossipConn:     gossipConn,
		ClientAddr:     clientAddr,
		ClientConn:     clientConn,
		Name:           namee,
		Peers:          peers,
		SimpleMode:     simpleMode,
		RumorsReceived: make(map[string][]*packets.RumorMessage),
		PendingAcks:    make(map[string][]packets.PeerStatus),
	}
}

func handleClient(gossiper *nodes.Gossiper, message []byte, rlen int) {
	var msg packets.Message
	protobuf.Decode(message[:rlen], &msg)

	if gossiper.SimpleMode {
		packet := packets.GossipPacket{
			Simple: &packets.SimpleMessage{
				OriginalName:  gossiper.Name,
				RelayPeerAddr: gossiper.RelayAddress(),
				Contents:      msg.Text,
			}}
		sourceAddress := packet.Simple.RelayPeerAddr
		packet.Simple.RelayPeerAddr = gossiper.RelayAddress()
		packet.Simple.OriginalName = gossiper.Name

		gossiper.SimpleBroadcast(packet, sourceAddress)
		fmt.Printf("CLIENT MESSAGE %s\n", packet.Simple.Contents)
		fmt.Println("PEERS ", strings.Join(gossiper.Peers[:], ","))
	} else {

		packet := packets.GossipPacket{
			Rumor: &packets.RumorMessage{
				Origin: gossiper.RelayAddress(),
				ID:     gossiper.GetNextRumorID(gossiper.Name),
				Text:   msg.Text},
		}
		gossiper.StoreRumor(packet)
		//Créer fonction rumorMonger
		//Dans cette fonction on crée une goroutine et alloue un channel + wait for 10 sec

		target := gossiper.SendPacketRandom(packet)
		fmt.Printf("MONGERING with %s\n", target)
	}
}

func listenClient(gossiper *nodes.Gossiper) {
	conn := gossiper.ClientConn
	defer conn.Close()
	for {
		message := make([]byte, 1000)
		rlen, _, err := conn.ReadFromUDP(message[:])
		if err != nil {
			panic(err)
		}
		go handleClient(gossiper, message, rlen)

	}
}
func logPeers(gossiper *nodes.Gossiper) {
	fmt.Println("PEERS ", strings.Join(gossiper.Peers[:], ","))
}

func logStatusPacket(packet *packets.StatusPacket, address string) {
	s := fmt.Sprintf("STATUS from %s", address)
	for _, status := range packet.Want {
		s += status.String()
	}
	fmt.Println(s)
}

func logRumor(rumor *packets.RumorMessage, peerAddr string) {
	fmt.Printf("RUMOR origin %s from %s contents %s\n",
		rumor.Origin,
		peerAddr,
		rumor.Text)
}

func logSimpleMessage(packet *packets.SimpleMessage) {
	fmt.Printf("SIMPLE MESSAGE origin %s from %s contents %s\n",
		packet.OriginalName,
		packet.RelayPeerAddr,
		packet.Contents)
}

func logMongering(target string) {
	fmt.Printf("MONGERING with %s\n", target)
}

func logSync(peerAddr string) {
	fmt.Printf("IN SYNC WITH %s\n", peerAddr)
}

func handleGossip(gossiper *nodes.Gossiper, message []byte, rlen int, raddr *net.UDPAddr) {
	var packet packets.GossipPacket
	protobuf.Decode(message[:rlen], &packet)
	logPeers(gossiper)
	peerAddr := fmt.Sprintf("%s:%d", raddr.IP, raddr.Port)

	//if packet.Simple.OriginalName != gossiper.name {
	if packet.Simple != nil {
		gossiper.AddPeer(packet.Simple.RelayPeerAddr)
		sourceAddress := packet.Simple.RelayPeerAddr
		packet.Simple.RelayPeerAddr = gossiper.RelayAddress()
		gossiper.SimpleBroadcast(packet, sourceAddress)
		logSimpleMessage(packet.Simple)
		logPeers(gossiper)
	} else if rumor := packet.Rumor; packet.Rumor != nil {

		logRumor(rumor, peerAddr)
		gossiper.AddPeer(peerAddr)
		gossiper.StoreRumor(packet)
		target := gossiper.SendPacketRandomExcept(packet, peerAddr)
		//gossiper.EnqueueForAck(target, packet.Rumor.Origin, packet.Rumor.ID+1)
		gossiper.SendPacket(packets.GossipPacket{StatusPacket: gossiper.GetStatusPacket()}, peerAddr)
		logMongering(target)

	} else if packet.StatusPacket != nil {
		//Il faut donc renvoyer le statusPacket à un channel du gossiper
		logStatusPacket(packet.StatusPacket, peerAddr)
		want := packet.StatusPacket.Want
		gossiperChoices := gossiper.CompareStatus(gossiper.GetStatus(), want)
		targetChoices := gossiper.CompareStatus(want, gossiper.GetStatus())
		mongeringAddresses := gossiper.AckStatusPacket(packet.StatusPacket, peerAddr, targetChoices == nil && gossiperChoices == nil && rand.Int()%2 == 0)

		if targetChoices != nil {
			idx := rand.Intn(len(targetChoices))
			choosenStatus := targetChoices[idx]
			rumor := gossiper.GetRumor(choosenStatus.Identifier, choosenStatus.NextID)
			packet := packets.GossipPacket{Rumor: rumor}
			gossiper.SendPacket(packet, peerAddr)
		} else if gossiperChoices != nil {
			statusPkt := packets.StatusPacket{gossiper.GetStatus()}
			gossiper.SendPacket(packets.GossipPacket{StatusPacket: &statusPkt}, peerAddr)
		} else {
			logSync(peerAddr)
			for _, ipport := range mongeringAddresses {
				logMongering(ipport)
			}
		}

	}
	//}

}

func listenGossip(gossiper *nodes.Gossiper) {
	conn := gossiper.GossipConn
	defer conn.Close()
	for {
		message := make([]byte, 1000)
		rlen, raddr, err := conn.ReadFromUDP(message[:])
		if err != nil {
			panic(err)
		}
		go handleGossip(gossiper, message, rlen, raddr)

	}
}
