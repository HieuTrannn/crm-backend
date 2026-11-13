-- Seed for docs/performance.md: tenant A with 100,000 contacts, tenant B with 20,000.
-- Runs as the non-superuser table owner, so FORCE RLS applies: set the tenant first.
\set ON_ERROR_STOP on
\set tenant_a '01900000-0000-7000-8000-00000000000a'
\set tenant_b '01900000-0000-7000-8000-00000000000b'
\set user_a   '01900000-0000-7000-8000-0000000000a1'
\set user_b   '01900000-0000-7000-8000-0000000000b1'

INSERT INTO tenants (id, name, slug) VALUES (:'tenant_a', 'Perf A', 'perf-a'), (:'tenant_b', 'Perf B', 'perf-b');

CREATE FUNCTION pg_temp.seed(p_tenant uuid, p_owner uuid, p_email text, n int) RETURNS void
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM set_config('app.tenant_id', p_tenant::text, true);
    INSERT INTO users (id, tenant_id, email, password_hash, full_name, role)
    VALUES (p_owner, p_tenant, p_email, 'x', 'Owner', 'owner');

    INSERT INTO contacts (id, tenant_id, full_name, email, phone, company, owner_id, created_at, updated_at, deleted_at)
    SELECT gen_random_uuid(), p_tenant,
           (ARRAY['Nguyễn','Trần','Lê','Phạm','Hoàng','Huỳnh','Phan','Vũ','Võ','Đặng','Bùi','Đỗ','Hồ','Ngô','Dương','Lý'])[1 + floor(random() * 16)::int]
           || ' ' || (ARRAY['Văn','Thị','Hữu','Minh','Ngọc','Thanh','Quốc','Gia','Đức','Thu'])[1 + floor(random() * 10)::int]
           || ' ' || (ARRAY['An','Bình','Chi','Dũng','Giang','Hà','Hải','Hạnh','Hoa','Hùng','Khoa','Lan','Linh','Long',
                            'Mai','Nam','Nga','Phúc','Quân','Sơn','Tâm','Thảo','Trang','Tuấn','Vy','Yến'])[1 + floor(random() * 26)::int],
           CASE WHEN g % 10 < 7 THEN 'kh' || g || '@example.vn' END,                       -- 70% have an email
           CASE WHEN g % 10 < 8 THEN '09' || lpad(((g::bigint * 7919) % 100000000)::text, 8, '0') END, -- 80% a phone
           CASE WHEN g % 3 = 0 THEN 'Công ty ' || (g % 500) END,
           p_owner, ts, ts,
           CASE WHEN g % 50 = 0 THEN ts + interval '1 day' END                            -- 2% soft-deleted
    FROM generate_series(1, n) AS g,
         LATERAL (SELECT now() - random() * interval '730 days' AS ts) AS t;
END $$;

BEGIN;
SELECT pg_temp.seed(:'tenant_a', :'user_a', 'owner@a.test', 100000);
SELECT pg_temp.seed(:'tenant_b', :'user_b', 'owner@b.test', 20000);
COMMIT;

VACUUM ANALYZE contacts;
