// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type orders struct {
	db                  *pgxpool.Pool
	inventory, payments string
}

type orderItem struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

type order struct {
	ID          string      `json:"id"`
	Items       []orderItem `json:"items"`
	AmountCents int         `json:"amountCents"`
	PaymentID   string      `json:"paymentId"`
	CreatedAt   time.Time   `json:"createdAt"`
}

func runOrders(ctx context.Context) error {
	shutdown, err := startTracing(ctx, "orders-api")
	if err != nil {
		return err
	}
	defer shutdown(context.Background()) //nolint:errcheck // spans in flight are best effort at exit

	cfg, err := pgxpool.ParseConfig(env("DEMO_DATABASE_URL", "postgres://shop:shop@localhost:5432/shop?sslmode=disable"))
	if err != nil {
		return fmt.Errorf("DEMO_DATABASE_URL: %w", err)
	}
	cfg.ConnConfig.Tracer = queryTracer{host: cfg.ConnConfig.Host, port: int(cfg.ConnConfig.Port), name: cfg.ConnConfig.Database}
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer db.Close()
	for {
		_, err = db.Exec(ctx, `CREATE TABLE IF NOT EXISTS orders (
			id text PRIMARY KEY, items jsonb NOT NULL, amount_cents integer NOT NULL,
			payment_id text NOT NULL, created_at timestamptz NOT NULL DEFAULT now())`)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(time.Second) // the database is starting
	}
	o := &orders{db: db, inventory: env("DEMO_INVENTORY_URL", "http://inventory-api:8080"), payments: env("DEMO_PAYMENTS_URL", "http://payments-api:8080")}
	return serve(ctx, env("DEMO_ADDR", ":8080"), o.routes())
}

func (o *orders) routes() *router {
	rt := newRouter()
	rt.handle("POST /orders", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Items []orderItem `json:"items"`
		}
		if err := decode(r, &in); err != nil || len(in.Items) == 0 {
			problem(w, r, http.StatusBadRequest, "Send the items as JSON, at least one.")
			return
		}
		ord := order{ID: "ord_" + randomID(), Items: in.Items}
		for _, it := range in.Items {
			var reserved struct {
				PriceCents int `json:"priceCents"`
			}
			err := call(r.Context(), http.MethodPost, o.inventory+"/reservations", it, &reserved, http.StatusCreated)
			var status *errStatus
			if errors.As(err, &status) && status.status == http.StatusConflict {
				problem(w, r, http.StatusConflict, it.SKU+" is out of stock.")
				return
			}
			if err != nil {
				problem(w, r, http.StatusBadGateway, "The inventory didn't reserve "+it.SKU+": "+err.Error())
				return
			}
			ord.AmountCents += reserved.PriceCents * it.Quantity
		}
		var paid struct {
			ID string `json:"id"`
		}
		if err := call(r.Context(), http.MethodPost, o.payments+"/payments", map[string]any{"orderId": ord.ID, "amountCents": ord.AmountCents}, &paid, http.StatusCreated); err != nil {
			problem(w, r, http.StatusBadGateway, "The payment didn't go through: "+err.Error())
			return
		}
		ord.PaymentID = paid.ID
		items, _ := json.Marshal(ord.Items)
		err := o.db.QueryRow(r.Context(), `INSERT INTO orders (id, items, amount_cents, payment_id) VALUES ($1, $2, $3, $4) RETURNING created_at`,
			ord.ID, items, ord.AmountCents, ord.PaymentID).Scan(&ord.CreatedAt)
		if err != nil {
			problem(w, r, http.StatusInternalServerError, "The order couldn't be saved.")
			return
		}
		writeJSON(w, http.StatusCreated, ord)
	})
	rt.handle("GET /orders/{id}", func(w http.ResponseWriter, r *http.Request) {
		var ord order
		var items []byte
		err := o.db.QueryRow(r.Context(), `SELECT id, items, amount_cents, payment_id, created_at FROM orders WHERE id = $1`, r.PathValue("id")).
			Scan(&ord.ID, &items, &ord.AmountCents, &ord.PaymentID, &ord.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			problem(w, r, http.StatusNotFound, "There is no order "+r.PathValue("id")+".")
			return
		}
		if err != nil {
			problem(w, r, http.StatusInternalServerError, "The order couldn't be read.")
			return
		}
		_ = json.Unmarshal(items, &ord.Items)
		writeJSON(w, http.StatusOK, ord)
	})
	rt.handle("GET /health", healthHandler("orders-api", check{"postgres:responseTime", func(ctx context.Context) (string, any, string, string) {
		start := time.Now()
		if err := o.db.Ping(ctx); err != nil {
			return "fail", nil, "", "The database doesn't answer."
		}
		return "pass", float64(time.Since(start).Microseconds()) / 1000, "ms", ""
	}}))
	return rt
}

// queryTracer makes a client span of each query, as OpenTelemetry's
// database conventions describe, so Signalbox draws the database on the
// canvas. Queries outside a trace, such as health checks, make none.
type queryTracer struct {
	host, name string
	port       int
}

func (q queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return ctx
	}
	operation, table := statement(data.SQL)
	ctx, _ = otel.Tracer("demoshop/pgx").Start(ctx, strings.TrimSpace(operation+" "+table),
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system.name", "postgresql"),
			attribute.String("db.namespace", q.name),
			attribute.String("db.operation.name", operation),
			attribute.String("db.collection.name", table),
			attribute.String("db.query.text", data.SQL),
			attribute.String("server.address", q.host),
			attribute.Int("server.port", q.port),
		))
	return ctx
}

func (q queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span := trace.SpanFromContext(ctx)
	if data.Err != nil && !errors.Is(data.Err, pgx.ErrNoRows) {
		span.RecordError(data.Err)
		span.SetStatus(codes.Error, data.Err.Error())
	}
	span.End()
}

// statement reads a query's operation and the table it works on, such as
// INSERT and orders.
func statement(sql string) (operation, table string) {
	fields := strings.Fields(sql)
	if len(fields) == 0 {
		return "", ""
	}
	operation = strings.ToUpper(fields[0])
	for i, f := range fields {
		switch strings.ToUpper(f) {
		case "FROM", "INTO", "UPDATE", "TABLE":
			if i+1 < len(fields) {
				name := strings.Trim(fields[i+1], "(")
				if strings.EqualFold(name, "IF") && i+4 < len(fields) {
					name = fields[i+4] // CREATE TABLE IF NOT EXISTS name
				}
				return operation, name
			}
		}
	}
	return operation, ""
}
