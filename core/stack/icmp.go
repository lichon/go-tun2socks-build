package stack

import (
	"inet.af/netstack/tcpip"
	"inet.af/netstack/tcpip/header"
)

func withICMPHandler() Option {
	return func(s *Stack) error {
		// Add default route table for IPv4 and IPv6.
		// This will handle all incoming ICMP packets.
		s.SetRouteTable([]tcpip.Route{
			{
				Destination: header.IPv4EmptySubnet,
				NIC:         s.nicID,
			},
			{
				Destination: header.IPv6EmptySubnet,
				NIC:         s.nicID,
			},
		})
		return nil
	}
}
