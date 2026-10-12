package main

import "testing"

func TestGeoProvidersParseCoords(t *testing.T) {
	cases := map[string]string{
		"ipwho.is":   `{"success":true,"ip":"1.2.3.4","country_code":"DE","city":"Frankfurt","latitude":50.11,"longitude":8.68,"connection":{"asn":24940,"isp":"Hetzner"}}`,
		"ipinfo.io":  `{"ip":"1.2.3.4","city":"Frankfurt","country":"DE","loc":"50.1100,8.6800","org":"AS24940 Hetzner Online GmbH"}`,
		"ip-api.com": `{"status":"success","query":"1.2.3.4","countryCode":"DE","city":"Frankfurt","lat":50.11,"lon":8.68,"isp":"Hetzner","as":"AS24940 Hetzner"}`,
	}
	for _, p := range geoProviders {
		raw, ok := cases[p.name]
		if !ok {
			t.Fatalf("no sample for %s", p.name)
		}
		g, err := p.parse([]byte(raw))
		if err != nil {
			t.Fatalf("%s: %v", p.name, err)
		}
		if g.Lat < 50.1 || g.Lat > 50.12 || g.Lon < 8.67 || g.Lon > 8.69 {
			t.Fatalf("%s: coords %v,%v", p.name, g.Lat, g.Lon)
		}
	}
}
