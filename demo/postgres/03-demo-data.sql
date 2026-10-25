-- A small demo shop for the LockLens lab: something real to browse in the
-- table explorer and to query in workloads. Fake data only.
CREATE SCHEMA shop AUTHORIZATION demo_app;
GRANT USAGE ON SCHEMA shop TO locklens_reader;
ALTER DEFAULT PRIVILEGES FOR ROLE demo_app IN SCHEMA shop GRANT SELECT ON TABLES TO locklens_reader;
SET ROLE demo_app;

CREATE TABLE shop.customers (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email      text NOT NULL UNIQUE,
    full_name  text NOT NULL,
    country    text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE shop.products (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sku         text NOT NULL UNIQUE,
    name        text NOT NULL,
    category    text NOT NULL,
    price_cents integer NOT NULL CHECK (price_cents > 0),
    attributes  jsonb NOT NULL DEFAULT '{}'
);
CREATE TABLE shop.orders (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    customer_id bigint NOT NULL REFERENCES shop.customers (id),
    status      text NOT NULL CHECK (status IN ('pending', 'paid', 'shipped', 'cancelled')),
    total_cents integer NOT NULL,
    note        text,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX orders_customer_idx ON shop.orders (customer_id);
CREATE INDEX orders_created_idx ON shop.orders (created_at);
CREATE TABLE shop.order_items (
    order_id    bigint NOT NULL REFERENCES shop.orders (id),
    product_id  bigint NOT NULL REFERENCES shop.products (id),
    quantity    integer NOT NULL CHECK (quantity > 0),
    price_cents integer NOT NULL,
    PRIMARY KEY (order_id, product_id)
);

INSERT INTO shop.customers (email, full_name, country, created_at)
SELECT 'customer' || g || '@example.test',
       (ARRAY['An', 'Binh', 'Chi', 'Dung', 'Giang', 'Hoa', 'Khanh', 'Lan', 'Minh', 'Nam'])[1 + g % 10] || ' Demo ' || g,
       (ARRAY['VN', 'SG', 'JP', 'DE', 'US'])[1 + g % 5],
       now() - (g || ' hours')::interval
FROM generate_series(1, 500) AS g;

INSERT INTO shop.products (sku, name, category, price_cents, attributes)
SELECT 'SKU-' || lpad(g::text, 4, '0'),
       (ARRAY['Coffee', 'Tea', 'Mug', 'Notebook', 'Backpack', 'Lamp', 'Chair', 'Desk'])[1 + g % 8] || ' model ' || g,
       (ARRAY['food', 'home', 'office'])[1 + g % 3],
       500 + (g * 137) % 20000,
       jsonb_build_object('color', (ARRAY['red', 'blue', 'black', 'white'])[1 + g % 4], 'weight_g', 100 + g * 7)
FROM generate_series(1, 200) AS g;

INSERT INTO shop.orders (customer_id, status, total_cents, note, created_at)
SELECT 1 + (g * 7) % 500,
       (ARRAY['pending', 'paid', 'paid', 'shipped', 'shipped', 'cancelled'])[1 + g % 6],
       0,
       CASE WHEN g % 9 = 0 THEN 'gift wrap' END,
       now() - (g || ' minutes')::interval
FROM generate_series(1, 5000) AS g;

INSERT INTO shop.order_items (order_id, product_id, quantity, price_cents)
SELECT o.id, 1 + (o.id * k * 13) % 200, 1 + (o.id + k) % 4, 500 + ((o.id * k) * 137) % 20000
FROM shop.orders AS o CROSS JOIN generate_series(1, 3) AS k
ON CONFLICT DO NOTHING;

UPDATE shop.orders AS o SET total_cents = s.total
FROM (SELECT order_id, sum(quantity * price_cents) AS total FROM shop.order_items GROUP BY order_id) AS s
WHERE s.order_id = o.id;

RESET ROLE;
ANALYZE;
