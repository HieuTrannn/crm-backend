# Hiệu năng truy vấn contacts (NFR-PERF-01, NFR-PERF-03)

Đo ngày 2026-11-13 trên PostgreSQL 17.11 (Docker, máy dev Windows). Số liệu dùng để so sánh
các phương án, không đại diện cho production.

## Cách đo

| Thành phần | Giá trị |
| --- | --- |
| Dữ liệu | Tenant A: 100.000 contact (98.000 còn sống, 2% đã xóa mềm). Tenant B: 20.000 contact |
| Nội dung | Họ + đệm + tên tiếng Việt ngẫu nhiên; 70% có email, 80% có số điện thoại |
| `created_at` | Ngẫu nhiên trong 2 năm gần nhất |
| Role chạy truy vấn | `app_user` (có RLS), trong transaction đã `set_config('app.tenant_id', ...)`, giống hệt API |
| Câu SQL | Đúng SQL sqlc sinh ra (`db/sqlc/contacts.sql.go`), chạy bằng `PREPARE` + `EXECUTE` có tham số như pgx |
| Lệnh | `EXPLAIN (ANALYZE, BUFFERS)`, lấy lần chạy thứ hai (cache đã nóng) |
| Kích thước | Bảng 22 MB, các index 22 MB |

Chạy lại (cần Docker và goose):

```bash
docker run -d --name crm-perf -e POSTGRES_PASSWORD=perf -e POSTGRES_DB=crm -p 55432:5432 postgres:17
docker exec crm-perf psql -U postgres -d crm -c "CREATE ROLE migrator LOGIN CREATEROLE PASSWORD 'migrator'; ALTER DATABASE crm OWNER TO migrator; ALTER SCHEMA public OWNER TO migrator;"
APP_USER_PASSWORD=perf_app goose -dir db/migrations postgres "postgres://migrator:migrator@localhost:55432/crm?sslmode=disable" up
docker exec -i crm-perf psql -U migrator -d crm < test/perf/contacts_seed.sql
docker exec -i crm-perf psql -U app_user -d crm < test/perf/contacts_explain.sql
docker rm -f crm-perf
```

Migration được chạy bằng role owner **không phải superuser** (giống Neon), vì superuser bỏ
qua RLS và sẽ cho ra kế hoạch khác hẳn với app.

## Tóm tắt

| # | Truy vấn | Kế hoạch | Thời gian | p95 < 100 ms? |
| --- | --- | --- | --- | --- |
| 1 | Danh sách, trang đầu | Index Scan `contacts_tenant_created_idx`, đọc 21 dòng | **0,06 ms** | Đạt |
| 2 | Danh sách, trang sau dòng 50.000 (keyset) | Index Scan, đọc 21 dòng | **0,07 ms** | Đạt |
| 3 | Cùng trang đó bằng `OFFSET 50000` (API **không** dùng) | Index Scan, đọc 50.021 dòng rồi bỏ đi | 25,8 ms | (so sánh) |
| 4 | Tìm `nguyen` (khớp ~6.000 dòng) | Seq Scan + top-N heapsort | **22,2 ms** | Đạt |
| 5 | Tìm `kh12345@` (khớp 1 dòng) | Seq Scan | **18,9 ms** | Đạt |
| 6 | Tìm `zzzzqq` (không khớp, trường hợp xấu nhất) | Seq Scan | **18,2 ms** | Đạt |

- **Phân trang keyset** (mục 1–2) đọc đúng 24 trang dữ liệu dù ở trang đầu hay trang thứ 2.500.
  `OFFSET` (mục 3) phải đọc và bỏ qua 50.000 dòng: chậm hơn ~400 lần và chậm dần theo số trang.
- **Tìm kiếm** (mục 4–6) luôn quét toàn bộ dữ liệu của tenant, vì dưới RLS không index nào giúp
  được `LIKE '%...%'` (xem [phần phát hiện](#phát-hiện-gin-trigram-index-không-được-dùng-dưới-rls)).
  Nhờ cột `search_text` tính sẵn lúc ghi, mỗi dòng chỉ còn một phép `LIKE` trên chuỗi có sẵn,
  nên quét 120.000 dòng mất khoảng 18–22 ms.

## Kết quả chi tiết

### 1. ListContacts, trang đầu (limit 20 + 1)

```
Limit  (cost=0.42..2.94 rows=21 width=196) (actual time=0.018..0.036 rows=21 loops=1)
  Buffers: shared hit=24
  ->  Result  (cost=0.42..11798.48 rows=98203 width=196) (actual time=0.018..0.035 rows=21 loops=1)
        One-Time Filter: ((NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid = '01900000-0000-7000-8000-00000000000a'::uuid)
        Buffers: shared hit=24
        ->  Index Scan using contacts_tenant_created_idx on contacts  (cost=0.42..11798.48 rows=98203 width=196) (actual time=0.014..0.028 rows=21 loops=1)
              Index Cond: ((tenant_id = '01900000-0000-7000-8000-00000000000a'::uuid) AND (ROW(created_at, id) < ROW('9999-12-31 00:00:00+00'::timestamp with time zone, 'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid)))
              Buffers: shared hit=24
Planning Time: 0.239 ms
Execution Time: 0.057 ms
```

### 2. ListContacts, trang sau dòng 50.000 (keyset)

```
Limit  (cost=0.42..8.44 rows=1 width=196) (actual time=0.012..0.031 rows=21 loops=1)
  Buffers: shared hit=24
  ->  Result  (cost=0.42..8.44 rows=1 width=196) (actual time=0.012..0.029 rows=21 loops=1)
        One-Time Filter: ((NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid = '01900000-0000-7000-8000-00000000000a'::uuid)
        ->  Index Scan using contacts_tenant_created_idx on contacts  (cost=0.42..8.44 rows=1 width=196) (actual time=0.009..0.024 rows=21 loops=1)
              Index Cond: ((tenant_id = '01900000-0000-7000-8000-00000000000a'::uuid) AND (ROW(created_at, id) < ROW('2025-04-11 01:09:07.955358+00'::timestamp with time zone, '7d12fa2a-55eb-49ac-8601-5452d8a47cd8'::uuid)))
              Buffers: shared hit=24
Planning Time: 0.090 ms
Execution Time: 0.065 ms
```

### 3. So sánh: cùng trang bằng OFFSET (không dùng trong API)

```
Limit  (cost=5882.39..5884.86 rows=21 width=196) (actual time=25.730..25.741 rows=21 loops=1)
  Buffers: shared hit=50469
  ->  Result  (actual time=0.011..24.287 rows=50021 loops=1)
        ->  Index Scan using contacts_tenant_created_idx on contacts  (actual time=0.008..17.772 rows=50021 loops=1)
              Index Cond: (tenant_id = '01900000-0000-7000-8000-00000000000a'::uuid)
              Buffers: shared hit=50469
Execution Time: 25.754 ms
```

### 4. SearchContacts `q=nguyen` (phổ biến)

```
Limit  (cost=5167.78..5167.83 rows=21 width=196) (actual time=22.157..22.158 rows=21 loops=1)
  Buffers: shared hit=2773
  ->  Sort  (cost=5167.78..5167.86 rows=31 width=196) (actual time=22.156..22.156 rows=21 loops=1)
        Sort Key: created_at DESC, id DESC
        Sort Method: top-N heapsort  Memory: 34kB
        ->  Result  (cost=0.01..5167.01 rows=31 width=196) (actual time=0.009..20.318 rows=6178 loops=1)
              One-Time Filter: ((NULLIF(current_setting('app.tenant_id'::text, true), ''::text))::uuid = '01900000-0000-7000-8000-00000000000a'::uuid)
              ->  Seq Scan on contacts  (cost=0.01..5167.01 rows=31 width=196) (actual time=0.006..19.741 rows=6178 loops=1)
                    Filter: ((deleted_at IS NULL) AND (tenant_id = '...a'::uuid) AND (ROW(created_at, id) < ROW(...)) AND (search_text ~~ '%nguyen%'::text))
                    Rows Removed by Filter: 113822
                    Buffers: shared hit=2767
Execution Time: 22.195 ms
```

### 5. SearchContacts `q=kh12345@` (hiếm)

```
Limit  (cost=5167.18..5167.20 rows=10 width=196) (actual time=18.933..18.934 rows=1 loops=1)
  ->  Sort  (actual time=18.932..18.933 rows=1 loops=1)
        Sort Key: created_at DESC, id DESC
        Sort Method: quicksort  Memory: 25kB
        ->  Result  (actual time=2.569..18.927 rows=1 loops=1)
              ->  Seq Scan on contacts  (actual time=2.549..18.906 rows=1 loops=1)
                    Filter: ((deleted_at IS NULL) AND (tenant_id = '...a'::uuid) AND (ROW(created_at, id) < ROW(...)) AND (search_text ~~ '%kh12345@%'::text))
                    Rows Removed by Filter: 119999
                    Buffers: shared hit=2767
Execution Time: 18.948 ms
```

### 6. SearchContacts `q=zzzzqq` (không khớp, trường hợp xấu nhất)

```
Limit  (actual time=18.193..18.194 rows=0 loops=1)
  ->  Sort  (actual time=18.192..18.193 rows=0 loops=1)
        ->  Result  (actual time=18.190..18.190 rows=0 loops=1)
              ->  Seq Scan on contacts  (actual time=18.186..18.186 rows=0 loops=1)
                    Filter: (... AND (search_text ~~ '%zzzzqq%'::text))
                    Rows Removed by Filter: 120000
                    Buffers: shared hit=2767
Execution Time: 18.208 ms
```

## Cách đọc EXPLAIN ANALYZE

Kế hoạch là một **cây**: node thụt vào sâu nhất chạy trước và đẩy dòng lên node cha. Đọc từ
trong ra ngoài, từ dưới lên trên. Ví dụ mục 5: `Seq Scan` đọc từng dòng → `Result` áp điều
kiện RLS → `Sort` sắp xếp → `Limit` lấy 21 dòng đầu.

| Thành phần | Ý nghĩa | Ví dụ ở trên |
| --- | --- | --- |
| `cost=0.42..2.94` | **Ước tính** của planner (đơn vị tùy ý): chi phí đến dòng đầu tiên..đến dòng cuối cùng. Chỉ dùng để so sánh các phương án với nhau | Mục 1 |
| `rows=98203` (trong ngoặc cost) | Số dòng planner **đoán** | Mục 1 đoán 98.203 nhưng `Limit` dừng sau 21 dòng |
| `actual time=0.014..0.028` | Thời gian **thật** (ms) đến dòng đầu..dòng cuối của node, **mỗi lần lặp** | |
| `rows=21 loops=1` | Số dòng **thật** node trả ra, và số lần node chạy. Tổng = rows × loops | |
| `Index Cond` | Điều kiện do **index** xử lý: chỉ đọc đúng phần cần | Mục 1–2: cả `tenant_id` lẫn `(created_at, id) < cursor` |
| `Filter` + `Rows Removed by Filter` | Điều kiện kiểm tra **sau khi** đã đọc dòng. Số bị loại lớn nghĩa là đọc thừa nhiều | Mục 6: đọc 120.000 dòng, giữ 0 |
| `Buffers: shared hit=24` | Số trang 8 KB đọc từ cache (`read=` là đọc từ đĩa). Thước đo khối lượng công việc, ổn định hơn thời gian | 24 trang (mục 1) so với 50.469 trang (mục 3) |
| `One-Time Filter` | Điều kiện chỉ tính **một lần** cho cả truy vấn | Chính là policy RLS, đã được rút gọn thành phép so sánh hằng |
| `Sort Method: top-N heapsort` | Sắp xếp trong RAM, chỉ giữ N dòng tốt nhất thay vì sắp xếp tất cả | Mục 4: 6.178 dòng khớp, giữ 21 |
| `Gather Merge`, `Workers Launched` | Truy vấn song song nhiều worker, rồi ghép kết quả đã sắp xếp | Xuất hiện ở lần đo trước khi có `search_text` |

Ba câu hỏi nên đặt ra khi đọc một kế hoạch:

1. **Có `Seq Scan` trên bảng lớn không?** Có thì tìm hiểu vì sao index không được dùng (NFR-PERF-03).
2. **`rows` ước tính có lệch nhiều so với `actual rows` không?** Lệch nhiều thường do thống kê cũ
   (chạy `ANALYZE`) hoặc điều kiện khó ước lượng (`LIKE '%...%'` luôn bị đoán rất thấp, như mục 4: đoán 31, thật 6.178).
3. **`Rows Removed by Filter` và `Buffers` có lớn so với số dòng trả về không?** Đó là công việc thừa.

## Phát hiện: GIN trigram index không được dùng dưới RLS

Mục 5.4 của PRD đề xuất GIN trigram index trên biểu thức `immutable_unaccent(lower(...))`. Lần
đo đầu tiên dùng đúng thiết kế đó:

| Tìm `kh12345@` chạy bằng | Kế hoạch | Thời gian |
| --- | --- | --- |
| Superuser (bỏ qua RLS) | `Bitmap Index Scan on contacts_search_idx`, đọc 2 dòng | **1,1 ms** |
| `app_user` (có RLS) | Parallel Seq Scan, lọc 120.000 dòng, gọi `unaccent` cho từng dòng | **119,6 ms** |
| `app_user`, ép `SET enable_seqscan = off` | Bitmap Scan theo `tenant_id`, vẫn lọc 98.000 dòng | 158 ms |

**Nguyên nhân.** Khi bảng có RLS, PostgreSQL chỉ cho một điều kiện của người dùng làm
`Index Cond` (tức là được chạy **trước** khi policy lọc) nếu mọi hàm trong điều kiện đó là
**LEAKPROOF**, nghĩa là không thể làm lộ giá trị của dòng qua thông báo lỗi hay hiệu ứng phụ.
Nếu không, một hàm như vậy có thể "nhìn thấy" dòng của tenant khác trước khi policy kịp loại nó.

Tra `pg_proc`: chỉ `texteq` (`=` trên text) là leakproof. `textlike` (`LIKE`), `texticlike`
(`ILIKE`), `textregexeq` (`~`), các toán tử trigram (`%`, `<%`, `%>`) và full-text search
(`@@`) đều **không**. Đánh dấu một hàm là LEAKPROOF cần quyền superuser, mà Neon không cấp.

**Các phương án đã cân nhắc:**

| Phương án | Tìm hiếm / không khớp | Tìm phổ biến | Đánh đổi |
| --- | --- | --- | --- |
| GIN trên biểu thức (PRD 5.4) | 120–158 ms | 9 ms | Index vô dụng với app, chỉ làm chậm ghi; không đạt NFR-PERF-01 |
| **Cột `search_text` sinh sẵn, không có GIN (đã chọn)** | **18–19 ms** | 22 ms | Không gọi `unaccent` lúc đọc; tốn thêm dung lượng; vẫn tăng tuyến tính theo số contact của tenant |
| Hàm `SECURITY DEFINER` chạy bằng role `BYPASSRLS` | ~1 ms (dùng GIN) | ~1 ms | Cần superuser để tạo role; tự lọc tenant trong hàm nên mất lớp bảo vệ thứ hai của RLS |

**Giới hạn đã biết.** Thời gian tìm kiếm tăng tuyến tính: ước tính ~180 ms với 1 triệu contact
trong một tenant. Khi tới quy mô đó, cân nhắc phương án `SECURITY DEFINER` (nếu có superuser)
hoặc một search engine riêng (PRD mục 8).
