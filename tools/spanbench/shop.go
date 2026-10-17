// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CemTbi/Signalbox/internal/modules/telemetry"
)

// The generator makes traces from a model of a web shop, the demo from the
// build plan's canvas: twelve services that call each other, three
// PostgreSQL databases, Redis, Kafka and two outside providers. Spans carry
// the attributes OpenTelemetry's semantic conventions give them, with
// realistic cardinality: routes repeat, but paths, IDs, client addresses
// and SQL arguments vary. Random trace and span IDs, which don't compress,
// make up a good part of each span's size, as they do in real data.

// latency is a log-normal distribution given by its median and 99th
// percentile.
type latency struct{ median, p99 time.Duration }

func (l latency) draw(r *rand.Rand) time.Duration {
	sigma := math.Log(float64(l.p99)/float64(l.median)) / 2.326
	return time.Duration(float64(l.median) * math.Exp(sigma*r.NormFloat64()))
}

type language struct {
	name, sdkVersion, runtime, runtimeVersion, scopeName, scopeVersion string
	stacks                                                             []string
}

var languages = map[string]language{
	"go": {"go", "1.38.0", "go", "go1.27.2", "go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp", "0.63.0", []string{
		"goroutine 1 [running]:\nmain.(*handler).createOrder(0xc000120000, {0x1a2b3c0, 0xc0002a4000}, 0xc000136000)\n\t/app/internal/orders/handler.go:142 +0x1f4\nnet/http.HandlerFunc.ServeHTTP(...)\n\t/usr/local/go/src/net/http/server.go:2294\ngo.opentelemetry.io/contrib/instrumentation/net/http/otelhttp.(*middleware).serveHTTP(0xc0001a2000, {0x1a2b3c0, 0xc0002a4000}, 0xc000136000, {0x1a1f100, 0xc0001a8000})\n\t/go/pkg/mod/go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@v0.63.0/handler.go:179 +0xb2c",
		"goroutine 41 [running]:\ngithub.com/jackc/pgx/v5.(*Conn).Exec(0xc0003e4000, {0x1a2f1e8, 0xc000512000}, {0x17c4a2e, 0x41}, {0xc0004f6000, 0x3, 0x3})\n\t/go/pkg/mod/github.com/jackc/pgx/v5@v5.11.0/conn.go:432 +0x1c5\nmain.(*store).reserve(0xc0001b4000, {0x1a2f1e8, 0xc000512000}, {0xc00042a0f0, 0xc})\n\t/app/internal/inventory/store.go:88 +0x13e",
	}},
	"java": {"java", "1.54.0", "OpenJDK Runtime Environment", "21.0.8+9-LTS", "io.opentelemetry.tomcat-10.0", "2.20.0-alpha", []string{
		"com.example.payments.ProviderException: provider answered 502\n\tat com.example.payments.PspClient.charge(PspClient.java:118)\n\tat com.example.payments.PaymentService.pay(PaymentService.java:64)\n\tat com.example.payments.PaymentController.create(PaymentController.java:41)\n\tat java.base/jdk.internal.reflect.DirectMethodHandleAccessor.invoke(DirectMethodHandleAccessor.java:103)\n\tat org.springframework.web.servlet.FrameworkServlet.service(FrameworkServlet.java:885)\n\tat jakarta.servlet.http.HttpServlet.service(HttpServlet.java:658)\n\tat org.apache.catalina.core.ApplicationFilterChain.doFilter(ApplicationFilterChain.java:140)",
		"org.elasticsearch.client.ResponseException: method [POST], host [http://search-es:9200], URI [/products/_search], status line [HTTP/1.1 429 Too Many Requests]\n\tat org.elasticsearch.client.RestClient.convertResponse(RestClient.java:347)\n\tat org.elasticsearch.client.RestClient.performRequest(RestClient.java:313)\n\tat com.example.search.ProductSearch.query(ProductSearch.java:77)\n\tat com.example.search.SearchController.search(SearchController.java:33)",
	}},
	"nodejs": {"nodejs", "2.1.0", "nodejs", "v22.20.0", "@opentelemetry/instrumentation-http", "0.205.0", []string{
		"Error: upstream catalog-api answered 503\n    at CatalogClient.get (/app/src/clients/catalog.js:48:13)\n    at async ProductPage.render (/app/src/pages/product.js:22:19)\n    at async /app/node_modules/express/lib/router/layer.js:95:5",
		"TypeError: Cannot read properties of undefined (reading 'items')\n    at CartView.total (/app/src/pages/cart.js:61:28)\n    at CartView.render (/app/src/pages/cart.js:30:16)\n    at /app/node_modules/express/lib/router/route.js:149:13",
	}},
	"python": {"python", "1.37.0", "cpython", "3.13.7", "opentelemetry.instrumentation.fastapi", "0.58b0", []string{
		"Traceback (most recent call last):\n  File \"/app/cart/api.py\", line 57, in add_item\n    cart = await store.load(cart_id)\n  File \"/app/cart/store.py\", line 31, in load\n    raw = await redis.get(f\"cart:{cart_id}\")\n  File \"/usr/local/lib/python3.13/site-packages/redis/asyncio/client.py\", line 615, in execute_command\n    return await conn.retry.call_with_retry(\nredis.exceptions.TimeoutError: Timeout reading from redis:6379",
		"Traceback (most recent call last):\n  File \"/app/recommendations/model.py\", line 88, in score\n    vector = self.embeddings[user_id]\nKeyError: 'usr_01j9z3kq4m8r2t6v0x5w7y9a1b'",
	}},
}

type service struct {
	name, version string
	lang          language
	port          int
	pods          []pod
}

type pod struct {
	resource map[string]string
	ip       string
}

// step is something a route does after it receives a request.
type step struct {
	chance float64 // of happening at all; 0 means always
	// One of these.
	call     *route    // an HTTP call to another service
	db       *database // a query
	cache    string    // a Redis command
	external *outside  // an HTTP call to another company
	produce  string    // a Kafka topic to send a message to
}

type database struct {
	system, name, host string
	port               int
	queries            []string
	latency            latency
}

type outside struct {
	host, method, path string
	latency            latency
	errorRate          float64
}

type route struct {
	svc       *service
	method    string
	template  string
	path      func(r *rand.Rand) string
	self      latency // work the service does itself
	errorRate float64
	steps     []step
}

type shop struct {
	routes  []*route  // entry routes, called by people's browsers
	weights []float64 // cumulative
	worker  *route    // consumes order events
}

func newShop(r *rand.Rand) *shop {
	svc := func(name, version, lang string, pods int) *service {
		s := &service{name: name, version: version, lang: languages[lang], port: 8080}
		for i := range pods {
			pod := fmt.Sprintf("%s-%s-%s", name, randomHex(r, 5), randomHex(r, 5))
			s.pods = append(s.pods, newPod(s, pod, i, r))
		}
		return s
	}
	web := svc("storefront-web", "4.12.1", "nodejs", 4)
	catalog := svc("catalog-api", "2.8.0", "go", 3)
	search := svc("search-api", "1.19.3", "java", 2)
	cart := svc("cart-api", "3.2.7", "python", 3)
	orders := svc("orders-api", "2.3.0", "go", 3)
	payments := svc("payments-api", "1.8.1", "java", 2)
	inventory := svc("inventory-api", "0.14.2", "go", 2)
	users := svc("users-api", "5.0.4", "nodejs", 2)
	recs := svc("recommendations-api", "0.9.0", "python", 2)
	shipping := svc("shipping-api", "1.3.0", "go", 2)
	auth := svc("auth-api", "3.11.0", "go", 3)
	notify := svc("notifications-worker", "1.1.5", "go", 2)

	catalogDB := &database{"postgresql", "catalog", "catalog-db", 5432, []string{
		"SELECT id, name, price_cents, currency FROM products WHERE id = $1",
		"SELECT id, name, price_cents FROM products WHERE category_id = $1 ORDER BY rank LIMIT $2",
	}, latency{2 * time.Millisecond, 18 * time.Millisecond}}
	ordersDB := &database{"postgresql", "orders", "orders-db", 5432, []string{
		"INSERT INTO orders (id, user_id, total_cents, status) VALUES ($1, $2, $3, $4)",
		"INSERT INTO order_items (order_id, sku, quantity, price_cents) VALUES ($1, $2, $3, $4)",
		"SELECT id, user_id, total_cents, status, created_at FROM orders WHERE id = $1",
	}, latency{3 * time.Millisecond, 25 * time.Millisecond}}
	inventoryDB := &database{"postgresql", "inventory", "inventory-db", 5432, []string{
		"UPDATE stock SET reserved = reserved + $2 WHERE sku = $1 AND available - reserved >= $2",
		"SELECT available, reserved FROM stock WHERE sku = $1",
	}, latency{2 * time.Millisecond, 30 * time.Millisecond}}
	usersDB := &database{"postgresql", "users", "users-db", 5432, []string{
		"SELECT id, email, name, locale FROM users WHERE id = $1",
	}, latency{time.Millisecond, 9 * time.Millisecond}}
	es := &database{"elasticsearch", "products", "search-es", 9200, []string{
		`{"query":{"multi_match":{"query":"?","fields":["name^3","description"]}},"size":24}`,
	}, latency{12 * time.Millisecond, 140 * time.Millisecond}}

	psp := &outside{"psp.example", "POST", "/v2/charges", latency{220 * time.Millisecond, 1500 * time.Millisecond}, 0.015}
	carrier := &outside{"api.carrier.example", "POST", "/shipments", latency{90 * time.Millisecond, 700 * time.Millisecond}, 0.005}
	mail := &outside{"mail.example", "POST", "/v3/send", latency{60 * time.Millisecond, 400 * time.Millisecond}, 0.002}

	ms := time.Millisecond
	id := func(prefix string) func(*rand.Rand) string {
		return func(r *rand.Rand) string { return prefix + strconv.Itoa(100000+r.IntN(900000)) }
	}
	fixed := func(p string) func(*rand.Rand) string { return func(*rand.Rand) string { return p } }

	verify := &route{auth, "POST", "/tokens/verify", fixed("/tokens/verify"), latency{ms / 2, 4 * ms}, 0.0005,
		[]step{{cache: "GET"}}}
	getProduct := &route{catalog, "GET", "/products/{id}", id("/products/"), latency{ms, 8 * ms}, 0.002, []step{
		{cache: "GET"}, {chance: 0.3, db: catalogDB}, {chance: 0.3, cache: "SET"}}}
	listProducts := &route{catalog, "GET", "/products", fixed("/products"), latency{2 * ms, 15 * ms}, 0.002, []step{
		{db: catalogDB}}}
	searchRoute := &route{search, "GET", "/search", fixed("/search"), latency{4 * ms, 40 * ms}, 0.006, []step{
		{db: es}}}
	getUser := &route{users, "GET", "/users/{id}", id("/users/"), latency{ms, 6 * ms}, 0.001, []step{
		{db: usersDB}}}
	recommend := &route{recs, "GET", "/recommendations/{userId}", id("/recommendations/"), latency{8 * ms, 90 * ms}, 0.004, []step{
		{cache: "GET"}, {call: getProduct}, {call: getProduct}, {chance: 0.5, call: getProduct}}}
	getCart := &route{cart, "GET", "/carts/{id}", id("/carts/"), latency{ms, 10 * ms}, 0.003, []step{
		{cache: "GET"}}}
	addItem := &route{cart, "POST", "/carts/{id}/items", func(r *rand.Rand) string {
		return "/carts/" + strconv.Itoa(100000+r.IntN(900000)) + "/items"
	}, latency{2 * ms, 12 * ms}, 0.004, []step{{cache: "GET"}, {call: getProduct}, {cache: "SET"}}}
	stock := &route{inventory, "GET", "/inventory/{sku}", id("/inventory/SKU-"), latency{ms, 6 * ms}, 0.002, []step{
		{db: inventoryDB}}}
	reserve := &route{inventory, "POST", "/inventory/reservations", fixed("/inventory/reservations"), latency{2 * ms, 15 * ms}, 0.006, []step{
		{db: inventoryDB}, {db: inventoryDB}}}
	pay := &route{payments, "POST", "/payments", fixed("/payments"), latency{4 * ms, 30 * ms}, 0.004, []step{
		{external: psp}}}
	ship := &route{shipping, "POST", "/shipments", fixed("/shipments"), latency{3 * ms, 20 * ms}, 0.003, []step{
		{external: carrier}}}
	createOrder := &route{orders, "POST", "/orders", fixed("/orders"), latency{5 * ms, 40 * ms}, 0.003, []step{
		{call: verify}, {call: getUser}, {call: reserve}, {db: ordersDB}, {db: ordersDB}, {call: pay},
		{chance: 0.97, call: ship}, {produce: "order-events"}}}
	getOrder := &route{orders, "GET", "/orders/{id}", id("/orders/"), latency{ms, 9 * ms}, 0.002, []step{
		{call: verify}, {db: ordersDB}}}

	page := func(method, template string, path func(*rand.Rand) string, steps ...step) *route {
		return &route{web, method, template, path, latency{6 * ms, 60 * ms}, 0.002, steps}
	}
	s := &shop{worker: &route{notify, "", "process order-events", fixed(""), latency{3 * ms, 25 * ms}, 0.002, []step{
		{call: getUser}, {external: mail}}}}
	entries := []struct {
		r      *route
		weight float64
	}{
		{page("GET", "/products/{id}", id("/products/"), step{call: getProduct}, step{chance: 0.6, call: recommend}, step{chance: 0.5, call: stock}), 35},
		{page("GET", "/", fixed("/"), step{call: listProducts}, step{chance: 0.4, call: recommend}), 20},
		{page("GET", "/search", fixed("/search"), step{call: searchRoute}), 15},
		{page("GET", "/cart", fixed("/cart"), step{call: verify}, step{call: getCart}), 12},
		{page("POST", "/cart/items", fixed("/cart/items"), step{call: verify}, step{call: addItem}), 8},
		{page("POST", "/checkout", fixed("/checkout"), step{call: verify}, step{call: getCart}, step{call: createOrder}), 4},
		{page("GET", "/orders/{id}", id("/orders/"), step{call: getOrder}), 4},
		{page("GET", "/account", fixed("/account"), step{call: verify}, step{call: getUser}), 2},
	}
	var total float64
	for _, e := range entries {
		total += e.weight
		s.routes = append(s.routes, e.r)
		s.weights = append(s.weights, total)
	}
	for i := range s.weights {
		s.weights[i] /= total
	}
	return s
}

func newPod(s *service, name string, i int, r *rand.Rand) pod {
	nodes := []string{"ip-10-0-1-17.eu-central-1.compute.internal", "ip-10-0-2-140.eu-central-1.compute.internal",
		"ip-10-0-3-88.eu-central-1.compute.internal", "ip-10-0-1-203.eu-central-1.compute.internal"}
	return pod{
		ip: fmt.Sprintf("10.42.%d.%d", 1+r.IntN(30), 2+r.IntN(250)),
		resource: map[string]string{
			"service.name":                s.name,
			"service.version":             s.version,
			"service.namespace":           "shop",
			"service.instance.id":         randomHex(r, 8) + "-" + randomHex(r, 4) + "-4" + randomHex(r, 3) + "-a" + randomHex(r, 3) + "-" + randomHex(r, 12),
			"deployment.environment.name": "production",
			"host.name":                   name,
			"k8s.pod.name":                name,
			"k8s.namespace.name":          "shop",
			"k8s.deployment.name":         s.name,
			"k8s.node.name":               nodes[(i+r.IntN(len(nodes)))%len(nodes)],
			"container.id":                randomHex(r, 64),
			"telemetry.sdk.name":          "opentelemetry",
			"telemetry.sdk.language":      s.lang.name,
			"telemetry.sdk.version":       s.lang.sdkVersion,
			"process.runtime.name":        s.lang.runtime,
			"process.runtime.version":     s.lang.runtimeVersion,
			"cloud.provider":              "aws",
			"cloud.region":                "eu-central-1",
		},
	}
}

func randomHex(r *rand.Rand, n int) string {
	const digits = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = digits[r.IntN(16)]
	}
	return string(b)
}

var userAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 15_6) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (Linux; Android 16; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64; rv:144.0) Gecko/20100101 Firefox/144.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:144.0) Gecko/20100101 Firefox/144.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0",
	"Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
}

// wildNames is the share of server spans named after their URL path, such
// as "GET /orders/123456", instead of their route. Some instrumentation
// does that, and it gives span_name millions of values.
var wildNames float64

// generator turns the shop into spans.
type generator struct {
	r    *rand.Rand
	shop *shop
	env  uuid.UUID
	buf  []telemetry.Span
}

func newGenerator(seed uint64, env uuid.UUID) *generator {
	r := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)) //nolint:gosec // G404: seeded, so runs are reproducible.
	return &generator{r: r, shop: newShop(r), env: env}
}

// trace appends the spans of one request that starts at start, and
// returns them.
func (g *generator) trace(start time.Time) []telemetry.Span {
	g.buf = g.buf[:0]
	x := g.r.Float64()
	i := 0
	for i < len(g.shop.weights)-1 && x > g.shop.weights[i] {
		i++
	}
	var traceID telemetry.TraceID
	g.fill(traceID[:])
	clientIP := fmt.Sprintf("%d.%d.%d.%d", 1+g.r.IntN(223), g.r.IntN(256), g.r.IntN(256), 1+g.r.IntN(254))
	g.serve(g.shop.routes[i], traceID, telemetry.SpanID{}, start, clientIP, userAgents[g.r.IntN(len(userAgents))])
	return g.buf
}

func (g *generator) fill(b []byte) {
	var word [8]byte
	for i := 0; i < len(b); i += 8 {
		binary.LittleEndian.PutUint64(word[:], g.r.Uint64())
		copy(b[i:], word[:])
	}
}

func (g *generator) spanID() telemetry.SpanID {
	var id telemetry.SpanID
	g.fill(id[:])
	return id
}

// serve emits the server span of a route and everything it calls, and
// returns its duration and whether it failed.
func (g *generator) serve(rt *route, trace telemetry.TraceID, parent telemetry.SpanID, start time.Time, clientIP, userAgent string) (time.Duration, bool) {
	p := rt.svc.pods[g.r.IntN(len(rt.svc.pods))]
	id := g.spanID()
	index := len(g.buf)
	g.buf = append(g.buf, telemetry.Span{}) // filled in once its children are known

	cursor := start.Add(rt.self.draw(g.r) / 2)
	failed := false
	for _, st := range rt.steps {
		if st.chance > 0 && g.r.Float64() >= st.chance {
			continue
		}
		cursor = cursor.Add(time.Duration(20+g.r.IntN(200)) * time.Microsecond)
		d, bad := g.step(rt, p, st, trace, id, cursor)
		cursor = cursor.Add(d)
		failed = failed || bad && g.r.Float64() < 0.7
	}
	duration := cursor.Sub(start) + rt.self.draw(g.r)/2
	failed = failed || g.r.Float64() < rt.errorRate

	s := telemetry.Span{
		EnvironmentID: g.env, TraceID: trace, SpanID: id, ParentSpanID: parent, Start: start, Duration: duration,
		Service: rt.svc.name, Kind: telemetry.KindServer, ScopeName: rt.svc.lang.scopeName, ScopeVersion: rt.svc.lang.scopeVersion,
		ResourceAttributes: p.resource,
	}
	if rt.method == "" { // a consumer
		s.Name, s.Kind = rt.template, telemetry.KindConsumer
		s.Attributes = map[string]string{
			"messaging.system": "kafka", "messaging.operation.type": "process", "messaging.destination.name": "order-events",
			"messaging.consumer.group.name": "notifications", "messaging.kafka.offset": strconv.Itoa(g.r.IntN(50_000_000)),
		}
	} else {
		status := uint16(200)
		if rt.method == "POST" {
			status = 201
		}
		if strings.Contains(rt.template, "{") && g.r.Float64() < 0.01 {
			status = 404
		}
		if failed {
			status = []uint16{500, 502, 503}[g.r.IntN(3)]
		}
		path := rt.path(g.r)
		s.Name = rt.method + " " + rt.template
		if wildNames > 0 && g.r.Float64() < wildNames {
			s.Name = rt.method + " " + path
		}
		s.HTTPMethod, s.HTTPRoute, s.HTTPStatusCode = rt.method, rt.template, status
		s.Attributes = map[string]string{
			"http.request.method":       rt.method,
			"http.route":                rt.template,
			"url.path":                  path,
			"url.scheme":                "http",
			"http.response.status_code": strconv.Itoa(int(status)),
			"server.address":            rt.svc.name,
			"server.port":               strconv.Itoa(rt.svc.port),
			"client.address":            clientIP,
			"network.peer.address":      p.ip,
			"network.protocol.version":  "1.1",
			"user_agent.original":       userAgent,
			"http.response.body.size":   strconv.Itoa(200 + g.r.IntN(40_000)),
		}
		if rt.template == "/search" {
			s.Attributes["url.query"] = "q=" + []string{"shoes", "red+jacket", "usb-c+cable", "coffee", "lamp", "desk"}[g.r.IntN(6)]
		}
	}
	if failed {
		s.Status = telemetry.StatusError
		g.exception(&s, start.Add(duration*9/10), rt.svc.lang)
	}
	g.buf[index] = s
	return duration, failed
}

func (g *generator) exception(s *telemetry.Span, at time.Time, lang language) {
	stack := lang.stacks[g.r.IntN(len(lang.stacks))]
	firstLine, _, _ := strings.Cut(stack, "\n")
	s.StatusMessage = firstLine
	s.Events = append(s.Events, telemetry.Event{Time: at, Name: "exception", Attributes: map[string]string{
		"exception.type":       strings.TrimSpace(strings.SplitN(firstLine, ":", 2)[0]),
		"exception.message":    firstLine,
		"exception.stacktrace": stack,
	}})
}

// step emits the client span of one thing a route does, and returns its
// duration and whether it failed.
func (g *generator) step(rt *route, p pod, st step, trace telemetry.TraceID, parent telemetry.SpanID, start time.Time) (time.Duration, bool) {
	id := g.spanID()
	s := telemetry.Span{
		EnvironmentID: g.env, TraceID: trace, SpanID: id, ParentSpanID: parent, Start: start,
		Service: rt.svc.name, Kind: telemetry.KindClient, ScopeName: rt.svc.lang.scopeName, ScopeVersion: rt.svc.lang.scopeVersion,
		ResourceAttributes: p.resource,
	}
	failed := false
	switch {
	case st.call != nil:
		index := len(g.buf)
		g.buf = append(g.buf, telemetry.Span{})
		hop := time.Duration(150+g.r.IntN(400)) * time.Microsecond
		d, bad := g.serve(st.call, trace, id, start.Add(hop), p.ip, "Go-http-client/1.1")
		failed = bad
		status := g.buf[index+1].HTTPStatusCode
		s.Duration = d + 2*hop
		s.Name = st.call.method
		s.HTTPMethod, s.HTTPStatusCode, s.ServerAddress = st.call.method, status, st.call.svc.name
		s.Attributes = map[string]string{
			"http.request.method":       st.call.method,
			"url.full":                  fmt.Sprintf("http://%s:%d%s", st.call.svc.name, st.call.svc.port, st.call.path(g.r)),
			"server.address":            st.call.svc.name,
			"server.port":               strconv.Itoa(st.call.svc.port),
			"http.response.status_code": strconv.Itoa(int(status)),
			"network.protocol.version":  "1.1",
		}
		if failed {
			s.Status = telemetry.StatusError
		}
		g.buf[index] = s
		return s.Duration, failed
	case st.db != nil:
		q := st.db.queries[g.r.IntN(len(st.db.queries))]
		op, _, _ := strings.Cut(q, " ")
		s.Duration = st.db.latency.draw(g.r)
		s.Name = op + " " + st.db.name
		s.ServerAddress, s.DBSystem = st.db.host, st.db.system
		s.Attributes = map[string]string{
			"db.system.name":    st.db.system,
			"db.namespace":      st.db.name,
			"db.operation.name": op,
			"db.query.text":     q,
			"server.address":    st.db.host,
			"server.port":       strconv.Itoa(st.db.port),
		}
		if st.db.system == "elasticsearch" {
			s.Name = "search " + st.db.name
			s.Attributes["db.operation.name"] = "search"
		}
		failed = g.r.Float64() < 0.001
	case st.cache != "":
		key := []string{"session:", "product:", "cart:", "recs:"}[g.r.IntN(4)] + strconv.Itoa(100000+g.r.IntN(900000))
		s.Duration = latency{300 * time.Microsecond, 3 * time.Millisecond}.draw(g.r)
		s.Name = st.cache
		s.ServerAddress, s.DBSystem = "redis", "redis"
		s.Attributes = map[string]string{
			"db.system.name":    "redis",
			"db.operation.name": st.cache,
			"db.query.text":     st.cache + " " + key,
			"server.address":    "redis",
			"server.port":       "6379",
		}
	case st.external != nil:
		x := st.external
		s.Duration = x.latency.draw(g.r)
		failed = g.r.Float64() < x.errorRate
		status := uint16(200)
		if failed {
			status = []uint16{429, 500, 502, 503}[g.r.IntN(4)]
		}
		s.Name = x.method
		s.HTTPMethod, s.HTTPStatusCode, s.ServerAddress = x.method, status, x.host
		s.Attributes = map[string]string{
			"http.request.method":       x.method,
			"url.full":                  "https://" + x.host + x.path,
			"server.address":            x.host,
			"server.port":               "443",
			"http.response.status_code": strconv.Itoa(int(status)),
		}
	case st.produce != "":
		s.Kind = telemetry.KindProducer
		s.Duration = latency{2 * time.Millisecond, 20 * time.Millisecond}.draw(g.r)
		s.Name = "send " + st.produce
		s.Attributes = map[string]string{
			"messaging.system": "kafka", "messaging.operation.type": "send", "messaging.destination.name": st.produce,
			"messaging.kafka.message.key": strconv.Itoa(100000 + g.r.IntN(900000)),
		}
		g.buf = append(g.buf, s)
		// The worker picks the message up later, in the same trace.
		g.serve(g.shop.worker, trace, id, start.Add(s.Duration+time.Duration(20+g.r.IntN(400))*time.Millisecond), p.ip, "")
		return s.Duration, false
	}
	if failed {
		s.Status = telemetry.StatusError
	}
	g.buf = append(g.buf, s)
	return s.Duration, failed
}
