// Copyright 2026 Mehmet ERGULCU and the abarow contributors
// SPDX-License-Identifier: Apache-2.0

// Command natsauth produces the identity and permission configuration of the abarow NATS
// servers: an NKey for each service (the private key file <out>/<service>/nats.nk)
// and the server's configuration (<out>/nats.conf: public keys and permissions).
//
// Register (the default role). If -leaf is given the register connects to the store server as a leaf, with TLS;
// the store server's certificate is verified with the store's CA (-leaf-ca). The connection's
// key is <out>/leaf/nats.nk, and its public part is added to the back
// house (-show-leaf-key prints it):
//
//	natsauth -out /auth -users sales,staff,register-sim -server-name register-1 [-leaf tls://store:7421 -leaf-ca ca.pem]
//	natsauth -out /auth -show-leaf-key
//
// Store server: the services' keys, the registers' leaf connections and the leaf
// TLS (<out>/tls: the store's CA and the server certificate). The CA certificate to be
// given to the registers is also in <out>/ca/ca.pem; it does not sit in the same folder as the
// private keys. The connected registers are in registers.json. If there is a headquarters
// the store server connects to it with -leaf, like a register:
//
//	natsauth -role store -out /auth -users report -server-name store-1 -registers /config/registers.json -leaf-hosts store,192.168.1.10 [-leaf tls://headquarters:7422 -leaf-ca ca.pem]
//	natsauth -registers /config/registers.json -add-register register-2 -register-key U... [-register-name "Register 2"]
//	natsauth -registers /config/registers.json -remove-register register-2
//
// Headquarters: the services' keys, the stores' (store servers')
// leaf connections and the leaf TLS. The connected stores are in stores.json:
//
//	natsauth -role headquarters -out /auth -users ingest -server-name headquarters -stores /config/stores.json -leaf-hosts headquarters.example.com
//	natsauth -stores /config/stores.json -add-store store-2 -store-key U... [-store-name "Kadıköy"]
//	natsauth -stores /config/stores.json -remove-store store-2
//
// Existing keys are kept (running it again is safe); the configuration
// is rewritten every time from the keys and the permission tables below. The private keys
// do not go into the repository: in the Docker simulation they are put on a volume at start,
// in a real install in Podman secrets.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/nats-io/nkeys"

	"abarow.com/pkg/nodes"
)

const (
	// salesStream is the stream of the sale events at the register (sales sets it up).
	salesStream = "ABAROW_SALES"
	// staffStream is the stream of the shift events at the register (staff sets it up).
	staffStream = "ABAROW_STAFF"
	// storeStream is the store stream on the store server fed from the registers' sales
	// streams (report sets it up).
	storeStream = "ABAROW_STORE"
	// headquartersStream is the headquarters stream fed from the stores' streams at
	// headquarters (ingest sets it up).
	headquartersStream = "ABAROW_HEADQUARTERS"
	// catalogStream is headquarters' catalog stream (catalog-admin sets it up);
	// the stores and registers take it from there.
	catalogStream = "ABAROW_CATALOG"
	// configStream is the store server's store settings stream (report sets
	// it up): store prices and cashiers; the registers take it from there.
	configStream = "ABAROW_STORE_CONFIG"
	// cashiersStream is the stream at the register that takes the store server's cashiers (the
	// config.cashier.> subjects of the store settings stream) as a source (staff sets it up).
	cashiersStream = "ABAROW_CASHIERS"
	// receiptStream is the stream of the delivery receipts (pkg/delivery): when the tier above
	// processes an event into the database it publishes the receipt, the tier below takes its
	// own receipts as a source into a copy of this stream and deletes only what
	// has a receipt. Each tier sets it up in its own NATS (sales and staff at the register,
	// report at the store server, ingest at headquarters).
	receiptStream = "ABAROW_RECEIPT"
	// transferStream is headquarters' transfer stream (headquarters' inventory sets it
	// up): the transfers sent to the stores and the list of places. Each
	// store's inventory takes as a source only what is sent to itself.
	transferStream = "ABAROW_TRANSFERS"
)

// permission is a NATS user's permissions.
type permission struct {
	Purpose   string
	Publish   []string
	Subscribe []string
}

// registerPermissions are the NATS permissions of the services at the register; with the
// principle of least permission. The permission list is only here. JetStream requests are
// publishes to the $JS.API.* subjects, the replies are subscriptions to _INBOX.*.
var registerPermissions = map[string]permission{
	"sales": {
		Purpose: "publishes the sale events, sets up the stream",
		Publish: []string{
			"sale.>",
			"$JS.API.INFO",
			"$JS.API.STREAM.CREATE." + salesStream,
			"$JS.API.STREAM.UPDATE." + salesStream,
			"$JS.API.STREAM.INFO." + salesStream,
			// Delivery receipt: sets up the stream, reads the receipts with its own durable consumer.
			"$JS.API.STREAM.CREATE." + receiptStream,
			"$JS.API.STREAM.UPDATE." + receiptStream,
			"$JS.API.STREAM.INFO." + receiptStream,
			"$JS.API.CONSUMER.CREATE." + receiptStream + ".sales-receipts",
			"$JS.API.CONSUMER.CREATE." + receiptStream + ".sales-receipts.>",
			"$JS.API.CONSUMER.INFO." + receiptStream + ".sales-receipts",
			"$JS.API.CONSUMER.MSG.NEXT." + receiptStream + ".sales-receipts",
			"$JS.ACK." + receiptStream + ".sales-receipts.>",
			// Deletes old events that have receipts: looks at the consumers, reads and deletes the message.
			"$JS.API.CONSUMER.LIST." + salesStream,
			"$JS.API.STREAM.MSG.GET." + salesStream,
			"$JS.API.STREAM.MSG.DELETE." + salesStream,
		},
		Subscribe: []string{"_INBOX.>"},
	},
	"staff": {
		Purpose: "reads the sale events with its own durable consumer (end of day, shift); publishes the shift events, sets up its stream; sets up and reads the stream that takes the store server's cashiers as a source",
		Publish: []string{
			"shift.>",
			"$JS.API.STREAM.CREATE." + staffStream,
			"$JS.API.STREAM.UPDATE." + staffStream,
			"$JS.API.STREAM.INFO." + staffStream,
			"$JS.API.INFO",
			"$JS.API.STREAM.INFO." + salesStream,
			"$JS.API.CONSUMER.CREATE." + salesStream + ".staff",
			"$JS.API.CONSUMER.CREATE." + salesStream + ".staff.>",
			"$JS.API.CONSUMER.INFO." + salesStream + ".staff",
			"$JS.API.CONSUMER.MSG.NEXT." + salesStream + ".staff",
			"$JS.ACK." + salesStream + ".staff.>",
			"$JS.API.STREAM.CREATE." + cashiersStream,
			"$JS.API.STREAM.UPDATE." + cashiersStream,
			"$JS.API.STREAM.INFO." + cashiersStream,
			"$JS.API.CONSUMER.CREATE." + cashiersStream + ".staff",
			"$JS.API.CONSUMER.CREATE." + cashiersStream + ".staff.>",
			"$JS.API.CONSUMER.INFO." + cashiersStream + ".staff",
			"$JS.API.CONSUMER.MSG.NEXT." + cashiersStream + ".staff",
			"$JS.ACK." + cashiersStream + ".staff.>",
			// Delivery receipt: sets up the stream, reads the receipts with its own durable consumer.
			"$JS.API.STREAM.CREATE." + receiptStream,
			"$JS.API.STREAM.UPDATE." + receiptStream,
			"$JS.API.STREAM.INFO." + receiptStream,
			"$JS.API.CONSUMER.CREATE." + receiptStream + ".staff-receipts",
			"$JS.API.CONSUMER.CREATE." + receiptStream + ".staff-receipts.>",
			"$JS.API.CONSUMER.INFO." + receiptStream + ".staff-receipts",
			"$JS.API.CONSUMER.MSG.NEXT." + receiptStream + ".staff-receipts",
			"$JS.ACK." + receiptStream + ".staff-receipts.>",
			// Deletes old events that have receipts: looks at the consumers, reads and deletes the message.
			"$JS.API.CONSUMER.LIST." + staffStream,
			"$JS.API.STREAM.MSG.GET." + staffStream,
			"$JS.API.STREAM.MSG.DELETE." + staffStream,
		},
		Subscribe: []string{"_INBOX.>"},
	},
	"catalog": {
		Purpose: "sets up the stream that takes headquarters' catalog from the store server as a source, reads the products with its own durable consumer",
		Publish: []string{
			"$JS.API.INFO",
			"$JS.API.STREAM.CREATE." + catalogStream,
			"$JS.API.STREAM.UPDATE." + catalogStream,
			"$JS.API.STREAM.INFO." + catalogStream,
			"$JS.API.CONSUMER.CREATE." + catalogStream + ".catalog",
			"$JS.API.CONSUMER.CREATE." + catalogStream + ".catalog.>",
			"$JS.API.CONSUMER.INFO." + catalogStream + ".catalog",
			"$JS.API.CONSUMER.MSG.NEXT." + catalogStream + ".catalog",
			"$JS.ACK." + catalogStream + ".catalog.>",
		},
		Subscribe: []string{"_INBOX.>"},
	},
	"register-sim": {
		Purpose: "simulation tool: reads the sales and shift streams, looks at the consumer state; cannot publish",
		// The first line of readStream ($JS.API.INFO) is in both lists.
		Publish:   append(readStream(salesStream), readStream(staffStream)[1:]...),
		Subscribe: []string{"_INBOX.>"},
	},
}

// operasyonPermissions, operasyondaki servislerin NATS yetkileridir.
var storePermissions = map[string]permission{
	"report": {
		Purpose: "builds the store stream from the registers' sales streams, reads the events with its own durable consumer (store reports); takes headquarters' catalog into the store for the registers and reads it; publishes the store prices and cashiers to the registers (store settings) and to headquarters (store stream)",
		Publish: []string{
			"$JS.API.INFO",
			"$JS.API.STREAM.CREATE." + storeStream,
			"$JS.API.STREAM.UPDATE." + storeStream,
			"$JS.API.STREAM.INFO." + storeStream,
			"$JS.API.STREAM.CREATE." + catalogStream,
			"$JS.API.STREAM.UPDATE." + catalogStream,
			"$JS.API.STREAM.INFO." + catalogStream,
			"$JS.API.CONSUMER.CREATE." + catalogStream + ".report",
			"$JS.API.CONSUMER.CREATE." + catalogStream + ".report.>",
			"$JS.API.CONSUMER.INFO." + catalogStream + ".report",
			"$JS.API.CONSUMER.MSG.NEXT." + catalogStream + ".report",
			"$JS.ACK." + catalogStream + ".report.>",
			"$JS.API.STREAM.CREATE." + configStream,
			"$JS.API.STREAM.UPDATE." + configStream,
			"$JS.API.STREAM.INFO." + configStream,
			"config.>",
			"store.>",
			"$JS.API.CONSUMER.CREATE." + storeStream + ".report",
			"$JS.API.CONSUMER.CREATE." + storeStream + ".report.>",
			"$JS.API.CONSUMER.INFO." + storeStream + ".report",
			"$JS.API.CONSUMER.MSG.NEXT." + storeStream + ".report",
			"$JS.ACK." + storeStream + ".report.>",
			// So that after restoring from a backup the events are processed from the start
			// (report replay).
			"$JS.API.CONSUMER.DELETE." + storeStream + ".report",
			// The receipts given to the registers (receipt.<register>.<stream>) are published
			// to the receipt stream.
			"receipt.>",
			// Delivery receipt: sets up the stream, reads the receipts with its own durable consumer.
			"$JS.API.STREAM.CREATE." + receiptStream,
			"$JS.API.STREAM.UPDATE." + receiptStream,
			"$JS.API.STREAM.INFO." + receiptStream,
			"$JS.API.CONSUMER.CREATE." + receiptStream + ".receipts",
			"$JS.API.CONSUMER.CREATE." + receiptStream + ".receipts.>",
			"$JS.API.CONSUMER.INFO." + receiptStream + ".receipts",
			"$JS.API.CONSUMER.MSG.NEXT." + receiptStream + ".receipts",
			"$JS.ACK." + receiptStream + ".receipts.>",
			// Deletes old events that have receipts: looks at the consumers, reads and deletes the message.
			"$JS.API.CONSUMER.LIST." + storeStream,
			"$JS.API.STREAM.MSG.GET." + storeStream,
			"$JS.API.STREAM.MSG.DELETE." + storeStream,
		},
		Subscribe: []string{"_INBOX.>"},
	},
	"catalog": {
		Purpose: "product and store price management at the store (catalog-admin): publishes the product's latest state and image to the catalog stream; report sets up the stream, this one only looks",
		Publish: []string{
			"$JS.API.INFO",
			"$JS.API.STREAM.INFO." + catalogStream,
			"catalog.product.>",
			"catalog.image.>",
		},
		Subscribe: []string{"_INBOX.>"},
	},
	"inventory": {
		Purpose: "reads the registers' sales from the store stream and headquarters' catalog with its own durable consumers (store stock); writes stock movements to the store stream (they go to headquarters); sets up and reads the stream that takes the transfers sent from headquarters to this store as a source",
		Publish: []string{
			"$JS.API.INFO",
			"store.stock.>",
			"$JS.API.STREAM.INFO." + storeStream,
			"$JS.API.CONSUMER.CREATE." + storeStream + ".inventory",
			"$JS.API.CONSUMER.CREATE." + storeStream + ".inventory.>",
			"$JS.API.CONSUMER.INFO." + storeStream + ".inventory",
			"$JS.API.CONSUMER.MSG.NEXT." + storeStream + ".inventory",
			"$JS.ACK." + storeStream + ".inventory.>",
			// So that after restoring from a backup the sales are processed from the start
			// (inventory replay).
			"$JS.API.CONSUMER.DELETE." + storeStream + ".inventory",
			"$JS.API.STREAM.INFO." + catalogStream,
			"$JS.API.CONSUMER.CREATE." + catalogStream + ".inventory",
			"$JS.API.CONSUMER.CREATE." + catalogStream + ".inventory.>",
			"$JS.API.CONSUMER.INFO." + catalogStream + ".inventory",
			"$JS.API.CONSUMER.MSG.NEXT." + catalogStream + ".inventory",
			"$JS.ACK." + catalogStream + ".inventory.>",
			"$JS.API.STREAM.CREATE." + transferStream,
			"$JS.API.STREAM.UPDATE." + transferStream,
			"$JS.API.STREAM.INFO." + transferStream,
			"$JS.API.CONSUMER.CREATE." + transferStream + ".inventory",
			"$JS.API.CONSUMER.CREATE." + transferStream + ".inventory.>",
			"$JS.API.CONSUMER.INFO." + transferStream + ".inventory",
			"$JS.API.CONSUMER.MSG.NEXT." + transferStream + ".inventory",
			"$JS.ACK." + transferStream + ".inventory.>",
		},
		Subscribe: []string{"_INBOX.>"},
	},
	"register-sim": {
		Purpose:   "simulation tool: reads the store stream; cannot publish",
		Publish:   readStream(storeStream),
		Subscribe: []string{"_INBOX.>"},
	},
}

// merkezPermissions, merkezdeki servislerin NATS yetkileridir.
var headquartersPermissions = map[string]permission{
	"inventory": {
		Purpose: "reads the stores' stock movements from the headquarters stream and headquarters' catalog with its own durable consumers (stock of all places); sets up the transfer stream, publishes the transfers sent to the stores and the list of places",
		Publish: []string{
			"$JS.API.INFO",
			"$JS.API.STREAM.INFO." + headquartersStream,
			"$JS.API.CONSUMER.CREATE." + headquartersStream + ".inventory",
			"$JS.API.CONSUMER.CREATE." + headquartersStream + ".inventory.>",
			"$JS.API.CONSUMER.INFO." + headquartersStream + ".inventory",
			"$JS.API.CONSUMER.MSG.NEXT." + headquartersStream + ".inventory",
			"$JS.ACK." + headquartersStream + ".inventory.>",
			"$JS.API.STREAM.INFO." + catalogStream,
			"$JS.API.CONSUMER.CREATE." + catalogStream + ".inventory",
			"$JS.API.CONSUMER.CREATE." + catalogStream + ".inventory.>",
			"$JS.API.CONSUMER.INFO." + catalogStream + ".inventory",
			"$JS.API.CONSUMER.MSG.NEXT." + catalogStream + ".inventory",
			"$JS.ACK." + catalogStream + ".inventory.>",
			"$JS.API.STREAM.CREATE." + transferStream,
			"$JS.API.STREAM.UPDATE." + transferStream,
			"$JS.API.STREAM.INFO." + transferStream,
			"transfer.>",
		},
		Subscribe: []string{"_INBOX.>"},
	},
	"ingest": {
		Purpose: "builds the headquarters stream from the stores' streams, reads the events with its own durable consumer (writes to PostgreSQL); on restoring from a backup it deletes its consumer and reads the stream from the start",
		Publish: []string{
			"$JS.API.INFO",
			"$JS.API.STREAM.CREATE." + headquartersStream,
			"$JS.API.STREAM.UPDATE." + headquartersStream,
			"$JS.API.STREAM.INFO." + headquartersStream,
			"$JS.API.CONSUMER.CREATE." + headquartersStream + ".ingest",
			"$JS.API.CONSUMER.CREATE." + headquartersStream + ".ingest.>",
			"$JS.API.CONSUMER.INFO." + headquartersStream + ".ingest",
			"$JS.API.CONSUMER.DELETE." + headquartersStream + ".ingest",
			"$JS.API.CONSUMER.MSG.NEXT." + headquartersStream + ".ingest",
			"$JS.ACK." + headquartersStream + ".ingest.>",
			// The receipts given to the stores (receipt.<store>.abarow_store) are published
			// to the receipt stream; the stores take them from there.
			"$JS.API.STREAM.CREATE." + receiptStream,
			"$JS.API.STREAM.UPDATE." + receiptStream,
			"$JS.API.STREAM.INFO." + receiptStream,
			"receipt.>",
		},
		Subscribe: []string{"_INBOX.>"},
	},
	"catalog": {
		Purpose: "publishes headquarters' catalog (products, headquarters price schedule, product images) to the catalog stream; the stores and registers take it from there",
		Publish: []string{
			"$JS.API.INFO",
			"$JS.API.STREAM.CREATE." + catalogStream,
			"$JS.API.STREAM.UPDATE." + catalogStream,
			"$JS.API.STREAM.INFO." + catalogStream,
			"catalog.product.>",
			"catalog.image.>",
		},
		Subscribe: []string{"_INBOX.>"},
	},
	"register-sim": {
		Purpose:   "simulation tool: reads the headquarters stream; cannot publish",
		Publish:   readStream(headquartersStream),
		Subscribe: []string{"_INBOX.>"},
	},
}

// readStream is the permission to read a stream with temporary consumers.
func readStream(stream string) []string {
	return []string{
		"$JS.API.INFO",
		"$JS.API.STREAM.INFO." + stream,
		"$JS.API.CONSUMER.CREATE." + stream + ".>",
		"$JS.API.CONSUMER.INFO." + stream + ".>",
		"$JS.API.CONSUMER.MSG.NEXT." + stream + ".>",
		"$JS.API.CONSUMER.DELETE." + stream + ".>",
	}
}

// role, bir NATS sunucusunun katmandaki yeridir.
type role struct {
	Title       string
	Permissions map[string]permission
	Default     []string
	// Downstream: the devices that connect from below as leaf (register or store);
	// if empty the server does not listen for leaf connections. ListFlag is the flag of the
	// device list, Sources are the streams the tier above takes as a source from these devices
	// (upward flow: sales). Downward are the streams the devices take as a source from this
	// server (downward flow: catalog).
	Downstream, ListFlag string
	Sources, Downward    []string
	// LeafPort is the port where the devices below listen for leaf connections:
	// store server 7421 (registers), headquarters 7422 (stores). Since they are separate
	// the two tiers can run on the same machine too, and which one is connected to is
	// understood from the address.
	LeafPort int
	// Upstream: the tier above that can be connected to with -leaf; if empty it does not connect.
	Upstream string
}

// roles are the roles' permission tables and default services.
var roles = map[string]role{
	"register": {Title: "register", Permissions: registerPermissions, Default: []string{"sales", "staff"}, Upstream: "store"},
	"store": {Title: "store", Permissions: storePermissions, Default: []string{"report", "inventory"},
		Downstream: "register", ListFlag: "-registers", Sources: []string{salesStream, staffStream}, Downward: []string{catalogStream, configStream, receiptStream}, LeafPort: 7421, Upstream: "headquarters"},
	"headquarters": {Title: "headquarters", Permissions: headquartersPermissions, Default: []string{"ingest", "catalog", "inventory"},
		Downstream: "store", ListFlag: "-stores", Sources: []string{storeStream}, Downward: []string{catalogStream, transferStream, receiptStream}, LeafPort: 7422},
}

// appSubjects are the application subjects the services at the register publish (the
// publish permissions in the permission table other than $ and _INBOX). These subjects do not
// pass through the register's leaf connection in either direction: otherwise through the store server a register's
// sale event would fall into another register's stream, and the end-of-day report would count
// another register's sales. Events go to the store server only through JetStream sourcing.
// When a new event subject is added with a publish permission it enters here automatically.
func appSubjects() []string {
	var subjects []string
	for _, p := range registerPermissions {
		for _, s := range p.Publish {
			if !strings.HasPrefix(s, "$") && !strings.HasPrefix(s, "_INBOX.") {
				subjects = append(subjects, s)
			}
		}
	}
	slices.Sort(subjects)
	return slices.Compact(subjects)
}

// leafPermissions are the permissions of the leaf connection of a device below (a register or a
// store's back house) to the tier above. NATS reads the leaf permissions from
// the device's point of view: publish is the messages going from the device to the tier above,
// subscribe is the messages going from the tier above to the device.
//
// Upward flow (streams): the tier above can open a source consumer on the device only for these
// streams. Downward flow (downward): the device can open a source consumer in the tier above
// (JetStream domain hub) only for these streams. Outside these the device cannot reach the tier above's
// JetStream API or the subjects of other devices. In both directions the deliveries are on
// $JS.S., the API replies on $JSC.R., the flow control on $JS.FC. subjects.
func leafPermissions(kind, id string, streams []string, hub string, downward []string) permission {
	api := "$JS." + id + ".API."
	p := permission{
		Purpose: kind + " " + id + " (leaf): " + strings.Join(streams, ", ") + " JetStream sourcing ile gelir",
		Publish: []string{
			"$JS.S.>",  // the events and signals the source consumer delivers
			"$JSC.R.>", // the device's JetStream API replies
			"$LDS.>",   // leaf loop detection
		},
	}
	for _, s := range streams {
		p.Subscribe = append(p.Subscribe,
			api+"CONSUMER.CREATE."+s,
			api+"CONSUMER.CREATE."+s+".>",
			api+"CONSUMER.DELETE."+s+".>")
	}
	p.Subscribe = append(p.Subscribe,
		"$JS.FC.>", // flow control replies
		"$LDS.>")
	if len(downward) > 0 {
		p.Purpose += "; " + strings.Join(downward, ", ") + " buradan cihaza iner"
		hubAPI := "$JS." + hub + ".API."
		for _, s := range downward {
			p.Publish = append(p.Publish,
				hubAPI+"CONSUMER.CREATE."+s,
				hubAPI+"CONSUMER.CREATE."+s+".>",
				hubAPI+"CONSUMER.DELETE."+s+".>")
		}
		p.Publish = append(p.Publish, "$JS.FC.>")
		p.Subscribe = append(p.Subscribe, "$JS.S.>", "$JSC.R.>")
	}
	return p
}

var conf = template.Must(template.New("nats.conf").Funcs(template.FuncMap{"quote": quoteList}).Parse(`# abarow {{.Title}} NATS yapılandırması: natsauth üretir, elle değiştirmeyin.
server_name: "{{.ServerName}}"
http_port: 8222
jetstream {
  store_dir: "/data"
  # JetStream alanı: üst katman bu sunucunun stream'lerine bu adla ulaşır.
  domain: "{{.ServerName}}"
}
{{- if or .Remote .Leafnodes}}
leafnodes {
{{- with .Leafnodes}}
  # Aşağıdan leaf bağlantıları ({{.Who}}): her biri kendi anahtarıyla,
  # yalnızca TLS ile.
  port: {{.Port}}
  tls {
    cert_file: "{{.Cert}}"
    key_file: "{{.Key}}"
  }
{{- end}}
{{- with .Remote}}
  # Üst katmana ({{.Who}}) leaf bağlantısı. Olaylar yalnızca JetStream
  # sourcing ile gider; uygulama konuları iki yönde de kapalıdır.
  remotes: [
    {
      url: "{{.URL}}"
      nkey: "{{.Seed}}"
      # Üst katmanın sertifikası onun CA'sıyla doğrulanır.
      tls { ca_file: "{{.CAFile}}" }
      deny_imports: [{{quote .Deny}}]
      deny_exports: [{{quote .Deny}}]
    }
  ]
{{- end}}
}
{{- end}}
authorization {
  users: [
{{- range .Users}}
    # {{.Name}}: {{.Purpose}}
    {
      nkey: "{{.PublicKey}}"
      allowed_connection_types: [{{quote .ConnectionTypes}}]
      permissions: {
        publish: { allow: [{{quote .Publish}}] }
        subscribe: { allow: [{{quote .Subscribe}}] }
      }
    }
{{- end}}
  ]
}
`))

func quoteList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = `"` + s + `"`
	}
	return strings.Join(q, ", ")
}

type user struct {
	Name, Purpose, PublicKey string
	Publish, Subscribe       []string
	// ConnectionTypes: services can connect only as clients (STANDARD), registers
	// only as leaf (LEAFNODE).
	ConnectionTypes []string
}

type remote struct {
	Who, URL, Seed, CAFile string
	Deny                   []string
}

type leafnodes struct {
	Who       string
	Port      int
	Cert, Key string
}

// config is natsauth's settings.
type config struct {
	Role       string   // register, store or headquarters
	Out        string   // output folder
	Users      []string // services; if empty the role's defaults
	ServerName string   // NATS server name and JetStream domain
	Leaf       string   // register, store: the tier above's leaf address (tls://)
	LeafCA     string   // the tier above's CA certificate (there ca/ca.pem)
	Registers  string   // operasyon: registers.json
	Stores     string   // merkez: stores.json
	LeafHosts  []string // store, headquarters: the names and IPs connected to from below
	// ConfDir is the path of the output folder in the NATS container;
	// the file paths in the configuration are written accordingly.
	ConfDir string
}

func main() {
	var c config
	flag.StringVar(&c.Role, "role", "register", "the server's role: register, store or headquarters")
	flag.StringVar(&c.Out, "out", "auth", "output folder")
	users := flag.String("users", "", "comma-separated services; if empty the role's defaults (register: sales,staff; store: report; headquarters: ingest,catalog)")
	flag.StringVar(&c.ServerName, "server-name", "register-1", "NATS server name and JetStream domain: the register id at a register, the store id at a store server, e.g. headquarters at headquarters")
	flag.StringVar(&c.Leaf, "leaf", "", "register: the store server's, store: headquarters' leaf address (e.g. tls://store:7421); if empty it does not connect to the tier above")
	flag.StringVar(&c.LeafCA, "leaf-ca", "", "CA certificate of the tier above (there it is <out>/ca/ca.pem); required with -leaf")
	flag.StringVar(&c.Registers, "registers", "", "store: the list of connected registers (registers.json)")
	flag.StringVar(&c.Stores, "stores", "", "headquarters: the list of connected stores (stores.json)")
	leafHosts := flag.String("leaf-hosts", "store", "store, headquarters: the names and IPs to be connected to from below, comma-separated (written to the server certificate)")
	flag.StringVar(&c.ConfDir, "conf-dir", "/auth", "the path of the output folder in the NATS container")
	showLeafKey := flag.Bool("show-leaf-key", false, "register, store: prints the public part of the leaf key (generates it if missing); this is the key to add to the tier above")
	addRegister := flag.String("add-register", "", "adds a register with this id to registers.json or changes its key")
	registerKey := flag.String("register-key", "", "-add-register: the register's public leaf key (-show-leaf-key prints it at the register)")
	registerName := flag.String("register-name", "", "-add-register: the name shown on the screen")
	removeRegister := flag.String("remove-register", "", "removes the register with this id from registers.json (the register can no longer connect)")
	addStore := flag.String("add-store", "", "adds a store with this id to stores.json or changes its key")
	storeKey := flag.String("store-key", "", "-add-store: the public leaf key of the store's store server (-show-leaf-key prints it at the store server)")
	storeName := flag.String("store-name", "", "-add-store: the name shown on the screen")
	removeStore := flag.String("remove-store", "", "removes the store with this id from stores.json (the store can no longer connect)")
	flag.Parse()
	if *users != "" {
		c.Users = strings.Split(*users, ",")
	}
	for _, h := range strings.Split(*leafHosts, ",") {
		if h = strings.TrimSpace(h); h != "" {
			c.LeafHosts = append(c.LeafHosts, h)
		}
	}

	var err error
	switch {
	case *showLeafKey:
		var pub string
		if pub, err = ensureKey(filepath.Join(c.Out, "leaf", "nats.nk")); err == nil {
			fmt.Println(pub)
		}
	case *addRegister != "":
		err = addNode(c.Registers, "-registers", "store server NATS and report", nodes.Node{ID: *addRegister, Name: *registerName, NKey: *registerKey})
	case *removeRegister != "":
		err = removeNode(c.Registers, "-registers", "store server NATS", *removeRegister)
	case *addStore != "":
		err = addNode(c.Stores, "-stores", "headquarters NATS and ingest", nodes.Node{ID: *addStore, Name: *storeName, NKey: *storeKey})
	case *removeStore != "":
		err = removeNode(c.Stores, "-stores", "headquarters NATS", *removeStore)
	default:
		err = run(c)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "natsauth:", err)
		os.Exit(1)
	}
}

// run generates the keys (if missing) and writes nats.conf.
func run(c config) error {
	role, ok := roles[c.Role]
	if !ok {
		return fmt.Errorf("bilinmeyen rol %q (kasa, operasyon ya da merkez)", c.Role)
	}
	if !nodes.ValidID(c.ServerName) {
		return fmt.Errorf("server name %q is invalid: letters, digits, - and _ are allowed (at most 63 characters)", c.ServerName)
	}
	names := c.Users
	if len(names) == 0 {
		names = role.Default
	}
	if c.ConfDir == "" {
		c.ConfDir = "/auth"
	}
	data := struct {
		Title, ServerName string
		Remote            *remote
		Leafnodes         *leafnodes
		Users             []user
	}{Title: role.Title, ServerName: c.ServerName}
	for _, name := range names {
		name = strings.TrimSpace(name)
		p, ok := role.Permissions[name]
		if !ok {
			return fmt.Errorf("bilinmeyen servis %q (bilinenler: %s)", name, strings.Join(known(role.Permissions), ", "))
		}
		pub, err := ensureKey(filepath.Join(c.Out, name, "nats.nk"))
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		data.Users = append(data.Users, user{Name: name, Purpose: p.Purpose, PublicKey: pub, Publish: p.Publish, Subscribe: p.Subscribe, ConnectionTypes: []string{"STANDARD"}})
	}

	// By the role's device list: the store server listens for registers, headquarters for
	// stores.
	list := map[string]string{"-registers": c.Registers, "-stores": c.Stores}
	for f, listPath := range list {
		if listPath != "" && f != role.ListFlag {
			return fmt.Errorf("%s is not used in this role (%s)", f, role.Title)
		}
	}

	// Leaf connection to the tier above (register → store server → headquarters).
	if c.LeafCA != "" && c.Leaf == "" {
		return errors.New("-leaf-ca is used only with -leaf")
	}
	if c.Leaf != "" {
		if role.Upstream == "" {
			return fmt.Errorf("-leaf is not used in this role (%s): there is no tier above", role.Title)
		}
		// The connection is always TLS: events do not go in the clear on the network.
		if u, err := url.Parse(c.Leaf); err != nil || u.Scheme != "tls" || u.Host == "" || u.User != nil {
			return fmt.Errorf("leaf address %q is invalid (e.g. tls://store:7421; the identity is not written into the address)", c.Leaf)
		}
		if c.LeafCA == "" {
			return fmt.Errorf("with -leaf the tier above's (%s) CA certificate (-leaf-ca) is required", role.Upstream)
		}
		if err := copyLeafCA(c.LeafCA, filepath.Join(c.Out, "leaf", caCertFile)); err != nil {
			return err
		}
		if _, err := ensureKey(filepath.Join(c.Out, "leaf", "nats.nk")); err != nil {
			return fmt.Errorf("leaf: %w", err)
		}
		seed, err := os.ReadFile(filepath.Join(c.Out, "leaf", "nats.nk"))
		if err != nil {
			return err
		}
		data.Remote = &remote{Who: role.Upstream, URL: c.Leaf, Seed: string(bytes.TrimSpace(seed)), CAFile: path.Join(c.ConfDir, "leaf", caCertFile), Deny: appSubjects()}
	}

	// Leaf connections from below: each device in the list with its own key.
	if role.Downstream != "" {
		listPath := list[role.ListFlag]
		if listPath == "" {
			return fmt.Errorf("%s is required for %s", role.Title, role.ListFlag)
		}
		devices, err := nodes.Load(listPath)
		if err != nil {
			return err
		}
		for _, n := range devices {
			if n.ID == c.ServerName {
				return fmt.Errorf("%s id %q cannot be the same as the server's name (the JetStream domains must be separate)", role.Downstream, n.ID)
			}
			if !nkeys.IsValidPublicUserKey(n.NKey) {
				return fmt.Errorf("%s %s: the key is invalid", role.Downstream, n.ID)
			}
			p := leafPermissions(role.Downstream, n.ID, role.Sources, c.ServerName, role.Downward)
			data.Users = append(data.Users, user{Name: n.ID, Purpose: p.Purpose, PublicKey: n.NKey, Publish: p.Publish, Subscribe: p.Subscribe, ConnectionTypes: []string{"LEAFNODE"}})
		}
		if err := ensureLeafTLS(filepath.Join(c.Out, "tls"), c.LeafHosts, c.ServerName, time.Now()); err != nil {
			return fmt.Errorf("leaf TLS: %w", err)
		}
		if err := copyLeafCA(filepath.Join(c.Out, "tls", caCertFile), filepath.Join(c.Out, "ca", caCertFile)); err != nil {
			return err
		}
		data.Leafnodes = &leafnodes{Who: role.Downstream + " (" + role.ListFlag[1:] + ")", Port: role.LeafPort,
			Cert: path.Join(c.ConfDir, "tls", serverCertFile), Key: path.Join(c.ConfDir, "tls", serverKeyFile)}
	}

	var b bytes.Buffer
	if err := conf.Execute(&b, data); err != nil {
		return err
	}
	// A read-only file cannot be overwritten; it is deleted first.
	path := filepath.Join(c.Out, "nats.conf")
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o444)
}

func known(table map[string]permission) []string {
	names := make([]string, 0, len(table))
	for n := range table {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// addNode adds the device (register or store) to the list; if a device with the same id
// exists it changes its key and name. It takes effect when the tier above's NATS and service
// (restart) are restarted.
func addNode(path, listFlag, restart string, r nodes.Node) error {
	if path == "" {
		return fmt.Errorf("%s is required", listFlag)
	}
	if !nkeys.IsValidPublicUserKey(r.NKey) {
		return errors.New("the key is invalid: it must be the key starting with U that natsauth -show-leaf-key prints on the device")
	}
	regs, err := nodes.Load(path)
	if err != nil {
		return err
	}
	if err := nodes.Save(path, nodes.Put(regs, r)); err != nil {
		return err
	}
	fmt.Printf("%s added; it can connect once %s is restarted\n", r.ID, restart)
	return nil
}

// removeNode removes the device from the list (e.g. a register was stolen, a store closed).
// When the tier above's NATS is restarted the device cannot connect; the events
// that have come stay in the tier above's stream.
func removeNode(path, listFlag, restart, id string) error {
	if path == "" {
		return fmt.Errorf("%s is required", listFlag)
	}
	regs, err := nodes.Load(path)
	if err != nil {
		return err
	}
	regs, ok := nodes.Remove(regs, id)
	if !ok {
		return fmt.Errorf("%s is not in the list", id)
	}
	if err := nodes.Save(path, regs); err != nil {
		return err
	}
	fmt.Printf("%s removed; it cannot connect once %s is restarted\n", id, restart)
	return nil
}

// ensureKey reads the private key file if it exists, otherwise generates and writes a new user
// key; it returns the public key. The file is bound only to this service's
// container; since the container runs as a non-root user it is written
// readable.
func ensureKey(path string) (string, error) {
	seed, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		kp, err := nkeys.CreateUser()
		if err != nil {
			return "", err
		}
		if seed, err = kp.Seed(); err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, seed, 0o444); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	kp, err := nkeys.FromSeed(bytes.TrimSpace(seed))
	if err != nil {
		return "", fmt.Errorf("the key file is corrupt (%s): %w", path, err)
	}
	return kp.PublicKey()
}
