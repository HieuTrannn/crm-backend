-- Used only by cmd/seed (local development and load tests).

-- Bulk contacts for load tests in one statement. Emails are unique per n, so
-- running the seed again inserts nothing new. created_at is spread over two
-- years so cursor pages and dashboards look realistic.
-- name: SeedContacts :execrows
INSERT INTO contacts (id, tenant_id, full_name, email, phone, company, owner_id, created_at, updated_at)
SELECT gen_random_uuid(),
       sqlc.arg(tenant_id),
       (ARRAY['Nguyễn','Trần','Lê','Phạm','Hoàng','Huỳnh','Phan','Vũ','Võ','Đặng'])[1 + g % 10]
         || ' ' || (ARRAY['Văn','Thị','Minh','Ngọc','Thanh','Quốc','Gia','Đức'])[1 + g % 8]
         || ' ' || (ARRAY['An','Bình','Chi','Dũng','Giang','Hà','Hải','Hoa','Khoa','Lan','Long','Mai','Nam','Thảo'])[1 + g % 14],
       'load' || g || '@' || sqlc.arg(email_domain)::text,
       '09' || lpad(g::text, 8, '0'),
       'Công ty ' || (g % 500),
       sqlc.arg(owner_id),
       now() - make_interval(days => g % 730, secs => g % 86400),
       now() - make_interval(days => g % 730, secs => g % 86400)
FROM generate_series(1, sqlc.arg(n)::int) AS g
ON CONFLICT (tenant_id, email) WHERE deleted_at IS NULL DO NOTHING;
