package metrics

import (
	"fmt"
	"time"

	influxdb "github.com/influxdata/influxdb-client-go/v2"
)

type InfluxDB struct {
	client       influxdb.Client
	organization string
}

func NewInfluxDB(host, key, organization string) *InfluxDB {
	return &InfluxDB{client: influxdb.NewClient("http://"+host, key)}
}

func (db *InfluxDB) LogLookup(bot string, d time.Duration, rec *time.Time, err bool) {
	api := db.client.WriteAPI(db.organization, "lookup")

	e := '0'
	if err {
		e = '1'
	}

	api.WriteRecord(fmt.Sprintf("lookup,bot=%s,error=%c duration=%d %d", bot, e, d.Milliseconds(), rec.UnixNano()))
}
