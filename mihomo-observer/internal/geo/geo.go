package geo

import (
	"embed"
	"encoding/json"
	"net/netip"
	"os"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"
	"mihomo-observer/internal/config"
)

//go:embed countries.json regions.json
var centersFS embed.FS

type Point struct {
	Latitude  float64 `json:"lat"`
	Longitude float64 `json:"lon"`
	Label     string  `json:"label"`
	Accuracy  string  `json:"accuracy"`
	Source    string  `json:"source"`
}

type center struct {
	Code, Name string
	Lat, Lon   float64
}

type Resolver struct {
	reader    *maxminddb.Reader
	countries map[string]center
	regions   map[string]center
	nodes     map[string]config.NodeLocation
	origin    *Point
	version   string
	problem   string
}

func Open(cfg config.Config) (*Resolver, error) {
	r := &Resolver{countries: map[string]center{}, regions: map[string]center{}, nodes: cfg.Map.Nodes}
	for _, spec := range []struct {
		name   string
		output map[string]center
	}{{"countries.json", r.countries}, {"regions.json", r.regions}} {
		b, err := centersFS.ReadFile(spec.name)
		if err != nil {
			return nil, err
		}
		var all []center
		if err = json.Unmarshal(b, &all); err != nil {
			return nil, err
		}
		for _, c := range all {
			spec.output[strings.ToUpper(c.Code)] = c
		}
	}
	if cfg.GeoIP.MMDBPath != "" {
		reader, err := maxminddb.Open(cfg.GeoIP.MMDBPath)
		if err != nil {
			r.problem = "GeoIP 資料庫無法讀取，請檢查 geoip.mmdb_path"
		} else {
			r.reader = reader
			if info, err := os.Stat(cfg.GeoIP.MMDBPath); err == nil {
				r.version = info.ModTime().UTC().Format("2006-01-02T15:04:05Z")
			}
		}
	}
	r.origin = r.Region(cfg.Map.Origin.Country, cfg.Map.Origin.Region)
	return r, nil
}

func (r *Resolver) Close() error {
	if r.reader != nil {
		return r.reader.Close()
	}
	return nil
}
func (r *Resolver) Version() string { return r.version }
func (r *Resolver) Problem() string { return r.problem }
func (r *Resolver) Origin() *Point  { return r.origin }

func (r *Resolver) Region(country, region string) *Point {
	country = strings.ToUpper(strings.TrimSpace(country))
	region = strings.ToUpper(strings.TrimSpace(region))
	if country == "" {
		return nil
	}
	if region != "" {
		if !strings.Contains(region, "-") {
			region = country + "-" + region
		}
		if c, ok := r.regions[region]; ok && strings.HasPrefix(region, country+"-") {
			return &Point{c.Lat, c.Lon, c.Name + " · " + country, "region", "manual"}
		}
	}
	if c, ok := r.countries[country]; ok {
		return &Point{c.Lat, c.Lon, c.Name, "country", "manual"}
	}
	return nil
}

func (r *Resolver) IP(ip string) *Point {
	if r.reader == nil {
		return nil
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.IsGlobalUnicast() || addr.IsPrivate() {
		return nil
	}
	var record struct {
		City struct {
			Name map[string]string `maxminddb:"names"`
			ID   uint              `maxminddb:"geoname_id"`
		} `maxminddb:"city"`
		Country struct {
			Code  string            `maxminddb:"iso_code"`
			Names map[string]string `maxminddb:"names"`
		} `maxminddb:"country"`
		Location struct {
			Latitude, Longitude *float64 `maxminddb:"latitude"`
		} `maxminddb:"location"`
		Subdivisions []struct {
			Code string `maxminddb:"iso_code"`
		} `maxminddb:"subdivisions"`
	}
	if err = r.reader.Lookup(addr).Decode(&record); err != nil || record.Country.Code == "" {
		return nil
	}
	label := record.Country.Names["en"]
	if label == "" {
		label = record.Country.Code
	}
	if (record.City.ID != 0 || len(record.City.Name) > 0) && record.Location.Latitude != nil && record.Location.Longitude != nil {
		city := record.City.Name["en"]
		if city != "" {
			label = city + " · " + label
		}
		return &Point{*record.Location.Latitude, *record.Location.Longitude, label, "city", "mmdb"}
	}
	if len(record.Subdivisions) > 0 {
		if p := r.Region(record.Country.Code, record.Subdivisions[0].Code); p != nil && p.Accuracy == "region" {
			p.Source = "mmdb+natural-earth"
			return p
		}
	}
	if p := r.Region(record.Country.Code, ""); p != nil {
		p.Source = "mmdb+natural-earth"
		return p
	}
	return nil
}

func (r *Resolver) Node(name string) map[string]*Point {
	loc, ok := r.nodes[name]
	if !ok {
		return map[string]*Point{"entry": nil, "exit": nil}
	}
	choose := func(ip, country, region string) *Point {
		if country != "" {
			return r.Region(country, region)
		}
		return r.IP(ip)
	}
	return map[string]*Point{"entry": choose(loc.EntryIP, loc.EntryCountry, loc.EntryRegion), "exit": choose(loc.ExitIP, loc.ExitCountry, loc.ExitRegion)}
}
