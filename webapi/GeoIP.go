/*
File Username:  GeoIP.go
Copyright:  2021 Peernet Foundation s.r.o.
Author:     Peter Kleissner

Support for the free MaxMind database 'GeoLite2 City'.
Information about the database: https://dev.maxmind.com/geoip/geolite2-free-geolocation-data

Potential libraries:
* https://github.com/IncSW/geoip2
* https://github.com/oschwald/maxminddb-golang

The IncSW lib was chosen because it has 0 dependencies - awesome!

*/

package webapi

import (
	"net"

	"github.com/IncSW/geoip2"
	"github.com/PeernetOfficial/core"
)

func (api *WebapiInstance) InitGeoIPDatabase(filename string) (err error) {
	api.geoipCityReader, err = geoip2.NewCityReaderFromFile(filename)
	return err
}

func (api *WebapiInstance) GeoIPLocation(IP net.IP) (latitude, longitude float64, valid bool) {
	if api.geoipCityReader == nil {
		return 0, 0, false
	}

	record, err := api.geoipCityReader.Lookup(IP)
	if err != nil {
		return 0, 0, false
	}

	return record.Location.Latitude, record.Location.Longitude, true
}

func (api *WebapiInstance) Peer2GeoIP(peer *core.PeerInfo) (latitude, longitude float64, valid bool) {
	if connection := peer.GetConnection2Share(false, true, true); connection != nil {
		return api.GeoIPLocation(connection.Address.IP)
	}

	return 0, 0, false
}
