-- seed.sql
-- Idempotent seed data: safe to run multiple times.
--
-- Prices are in PAISE (1 rupee = 100 paise); the rupee value is shown in a
-- comment beside each row for readability.
--
-- The reward program parameters (n = REWARD_MILESTONE_N, x =
-- REWARD_DISCOUNT_PERCENT) are deployment configuration read from the
-- environment; they are not stored in the database at all.

-- Customers. Authentication is out of scope, so the client identifies the
-- user by passing user_id when creating a cart; in production this would
-- come from the authenticated session.
INSERT INTO users (id, name, email) VALUES
    (1, 'Aarav Sharma', 'aarav@example.com'),
    (2, 'Priya Iyer',   'priya@example.com'),
    (3, 'Rohan Gupta',  'rohan@example.com')
ON CONFLICT (id) DO NOTHING;

INSERT INTO products (id, name, price_paise, inventory) VALUES
    (1, 'Wireless Mouse',          89900,  100), -- Rs      899.00
    (2, 'Mechanical Keyboard',    449900,   50), -- Rs    4,499.00
    (3, 'USB-C Hub',              199900,   75), -- Rs    1,999.00
    (4, '27" Monitor',           1899900,   20), -- Rs   18,999.00
    (5, 'Limited Edition Webcam',  249900,    3)  -- Rs    2,499.00 (limited stock)
ON CONFLICT (id) DO NOTHING;

-- These rows were inserted with explicit ids, so the identity sequences are
-- still at 1 and the next generated id would collide. Advance them past the
-- seeded rows.
SELECT setval('users_id_seq',    (SELECT MAX(id) FROM users));
SELECT setval('products_id_seq', (SELECT MAX(id) FROM products));
