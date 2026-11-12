# Mini CRM Multi-tenant — PRD, SRS & Kế hoạch

Nov 12, 2026 · @Hieu Tran

## 1. Tổng quan dự án

Mini CRM là một hệ thống quản lý khách hàng và cơ hội bán hàng dạng SaaS multi-tenant: nhiều công ty dùng chung một hệ thống, dữ liệu tách biệt hoàn toàn. Mục tiêu kép: học Go qua một dự án thật, và có một sản phẩm trên CV khớp với các công ty làm ERP/CRM/HRM SaaS.

**Nguyên tắc dẫn đường:** ít tính năng, làm sâu. Mỗi phần khó phải có test hoặc số liệu chứng minh.

### Mục tiêu

- Học và dùng thành thạo Go cho backend: HTTP API, PostgreSQL, concurrency, testing.
- Chứng minh 3 năng lực mà công ty SaaS quan tâm: cô lập tenant ở tầng database (Row-Level Security), xử lý đồng thời (import CSV bằng worker pool), chống ghi đè dữ liệu (optimistic locking).
- Có demo chạy thật trên internet, chi phí 0 đồng.
- Kiến trúc đủ sạch để mở rộng dần thành hệ thống doanh nghiệp (xem mục 8).

### Tech stack

| Tầng | Công nghệ | Ghi chú |
| --- | --- | --- |
| Ngôn ngữ backend | Go 1.27 | Bản ổn định hiện tại (tháng 11/2026) |
| HTTP framework | Gin | Phổ biến nhất trong JD tuyển Go, chạy trên net/http chuẩn |
| Truy cập DB | pgx v5 + sqlc | Viết SQL thuần, sqlc sinh code Go type-safe |
| Migration | goose (hoặc golang-migrate) | File SQL có up/down |
| Database | PostgreSQL 17 (Neon) | Dùng RLS, partial index, full-text search |
| Frontend | Next.js 16 (App Router), TypeScript | Tailwind CSS, shadcn/ui, TanStack Query, React Hook Form + Zod |
| Test | testing, testify, testcontainers-go, k6 | Integration test với Postgres thật |
| CI/CD | GitHub Actions, Docker | Lint, test, build, deploy tự động |
| Deploy | Vercel (FE), Render (BE), Neon (DB) | Đều có gói miễn phí, xem mục 6 |

### Phạm vi tài liệu

Tài liệu này gồm PRD (sản phẩm cần gì), SRS (hệ thống phải làm gì, đo thế nào), kiến trúc, kế hoạch triển khai và lộ trình mở rộng. Prompt cho Claude Code và tài liệu học Go nằm ở hai tài liệu riêng.

## 2. PRD — Tài liệu yêu cầu sản phẩm

Sản phẩm giúp đội bán hàng của doanh nghiệp nhỏ quản lý khách hàng và theo dõi cơ hội bán hàng trên một pipeline, thay cho Excel và tin nhắn rời rạc.

### Vấn đề

- Doanh nghiệp nhỏ lưu khách hàng trong Excel, mỗi sale một file, không ai biết tổng thể doanh số đang ở đâu.
- Hai người cùng sửa một file thì ghi đè lên nhau, mất dữ liệu.
- Chuyển từ Excel sang hệ thống mới tốn công nhập lại hàng nghìn dòng.
- CRM lớn (Salesforce, HubSpot) quá đắt và quá phức tạp cho đội 3–20 người.

### Persona

| Persona | Vai trò | Nhu cầu chính |
| --- | --- | --- |
| Chủ doanh nghiệp / Trưởng phòng kinh doanh | Owner của tenant | Xem tổng quan pipeline, doanh số dự kiến, quản lý thành viên |
| Nhân viên sale | Member | Thêm khách, cập nhật deal nhanh, không sợ bị người khác ghi đè |
| Nhà tuyển dụng (người xem demo) | Khách thăm | Vào demo bằng tài khoản có sẵn, thấy được giá trị trong 2 phút |

### User stories (MVP)

| Mã | Là ... | Tôi muốn ... | Để ... |
| --- | --- | --- | --- |
| US-01 | Chủ doanh nghiệp | Đăng ký công ty và tài khoản chủ trong một bước | Bắt đầu dùng ngay |
| US-02 | Người dùng | Đăng nhập, giữ phiên đăng nhập an toàn | Không phải đăng nhập lại liên tục |
| US-03 | Sale | Thêm, sửa, xóa, tìm kiếm khách hàng | Quản lý danh sách khách của công ty |
| US-04 | Sale | Tạo deal gắn với khách, kéo qua các giai đoạn | Theo dõi tiến độ bán hàng |
| US-05 | Chủ doanh nghiệp | Xem số lượng và tổng giá trị deal theo giai đoạn | Biết doanh số dự kiến |
| US-06 | Chủ doanh nghiệp | Import hàng nghìn khách từ file CSV, thấy tiến độ và dòng lỗi | Chuyển từ Excel sang nhanh |
| US-07 | Sale | Được cảnh báo khi người khác vừa sửa cùng deal | Không ghi đè mất thay đổi của đồng nghiệp |
| US-08 | Chủ doanh nghiệp | Chắc chắn công ty khác không bao giờ thấy dữ liệu của mình | Tin tưởng sử dụng |

### Trong và ngoài phạm vi MVP

| Trong MVP | Ngoài MVP (làm ở giai đoạn sau) |
| --- | --- |
| Đăng ký tenant, đăng nhập JWT, refresh token | Phân quyền nhiều vai trò chi tiết, mời thành viên qua email |
| CRUD khách hàng, phân trang, tìm kiếm | Ghi chú, công việc, lịch sử hoạt động |
| Deal theo pipeline cố định, chặn chuyển trạng thái sai | Pipeline tùy biến, trường tùy biến |
| Dashboard theo giai đoạn | Báo cáo nâng cao, xuất file |
| Import CSV bằng worker pool | Upload file đính kèm, gửi email |
| RLS, optimistic locking, k6, CI/CD | SSO, billing, audit log |

### Chỉ số thành công

- Demo chạy được trên internet, có tài khoản dùng thử, dữ liệu mẫu.
- Test tự động chứng minh tenant A không đọc được dữ liệu tenant B, kể cả khi code quên lọc `tenant_id`.
- Import 100.000 dòng CSV thành công, có số đo thời gian ghi trong README.
- p95 latency các API đọc dưới 100 ms ở 200 request/giây (đo bằng k6 trên máy local).
- Test coverage tầng service từ 70% trở lên.
- Bạn tự giải thích được mọi dòng code khi phỏng vấn.

## 3. SRS — Yêu cầu chức năng

Mỗi yêu cầu có mã để tham chiếu trong commit, test và prompt. “Phải” = bắt buộc cho MVP.

### 3.1 Xác thực và tenant (AUTH)

| Mã | Yêu cầu | Tiêu chí chấp nhận |
| --- | --- | --- |
| FR-AUTH-01 | Đăng ký tạo đồng thời tenant (tên công ty, slug duy nhất) và user có vai trò `owner`, trong một transaction | Lỗi ở bất kỳ bước nào thì không tạo gì cả; email trùng trả 409 |
| FR-AUTH-02 | Mật khẩu hash bằng bcrypt (cost ≥ 12), tối thiểu 8 ký tự | DB không chứa mật khẩu gốc |
| FR-AUTH-03 | Đăng nhập trả access token (JWT, hạn 15 phút) và refresh token (hạn 7 ngày), cả hai trong cookie httpOnly | Sai email hoặc mật khẩu trả cùng một thông báo 401 |
| FR-AUTH-04 | Refresh token xoay vòng (rotation): mỗi lần refresh cấp token mới, thu hồi token cũ; dùng lại token đã thu hồi thì thu hồi cả chuỗi | Lưu hash của refresh token trong DB, không lưu token gốc |
| FR-AUTH-05 | Đăng xuất thu hồi refresh token và xóa cookie | Gọi refresh sau đăng xuất trả 401 |
| FR-AUTH-06 | JWT chứa `sub` (user id), `tid` (tenant id), `role`; middleware đưa vào request context | Request không có hoặc sai token trả 401 |
| FR-AUTH-07 | Owner thêm thành viên (vai trò `member`) bằng email và mật khẩu tạm | Member không gọi được API quản lý thành viên (403) |

### 3.2 Khách hàng (CONTACT)

| Mã | Yêu cầu | Tiêu chí chấp nhận |
| --- | --- | --- |
| FR-CON-01 | Tạo khách hàng: họ tên (bắt buộc), email, số điện thoại, công ty, ghi chú, người phụ trách | Email hợp lệ nếu có; email trùng trong cùng tenant trả 409, khác tenant thì được phép |
| FR-CON-02 | Danh sách có phân trang kiểu cursor (`limit` mặc định 20, tối đa 100), sắp xếp mới nhất trước | Trả `next_cursor`; không trùng hoặc sót bản ghi khi có dữ liệu mới chèn vào |
| FR-CON-03 | Tìm kiếm theo tên, email, số điện thoại, không phân biệt hoa thường và dấu tiếng Việt (“nguyen” tìm ra “Nguyễn”) | Dùng extension `unaccent` + `pg_trgm` |
| FR-CON-04 | Xem chi tiết, sửa, xóa mềm (`deleted_at`) | Khách đã xóa không hiện trong danh sách; còn deal đang mở thì không cho xóa (409) |

### 3.3 Deal và pipeline (DEAL)

Trạng thái deal: `lead` → `qualified` → `proposal` → `won` hoặc `lost`. `won` là trạng thái kết thúc; `lost` chỉ được mở lại về `lead`.

| Từ | Được chuyển sang |
| --- | --- |
| lead | qualified, lost |
| qualified | proposal, lead, lost |
| proposal | won, lost, qualified |
| won | (không chuyển được) |
| lost | lead (mở lại, bắt buộc nhập lý do) |

| Mã | Yêu cầu | Tiêu chí chấp nhận |
| --- | --- | --- |
| FR-DEAL-01 | Tạo deal: tiêu đề, giá trị, khách hàng, ngày dự kiến chốt, người phụ trách; trạng thái ban đầu `lead` | Giá trị lưu bằng số nguyên (đơn vị VND), không dùng float; giá trị âm trả 400 |
| FR-DEAL-02 | Chuyển trạng thái chỉ theo bảng trên | Chuyển sai trả 422 kèm mã lỗi `INVALID_TRANSITION`; `lost` bắt buộc có `lost_reason` |
| FR-DEAL-03 | Mỗi lần chuyển trạng thái ghi một dòng vào `deal_stage_history` (từ, sang, ai, lúc nào) trong cùng transaction | Lịch sử khớp với trạng thái hiện tại |
| FR-DEAL-04 | Optimistic locking: mỗi deal có cột `version`; request sửa phải gửi `version` đã đọc | Version lệch trả 409 `VERSION_CONFLICT` kèm dữ liệu mới nhất; có test 2 goroutine sửa cùng lúc, đúng một cái thành công |
| FR-DEAL-05 | Danh sách deal lọc theo trạng thái, người phụ trách, khách hàng; dạng bảng và dạng Kanban | Phân trang cursor như FR-CON-02 |

### 3.4 Dashboard (DASH)

| Mã | Yêu cầu | Tiêu chí chấp nhận |
| --- | --- | --- |
| FR-DASH-01 | Số lượng và tổng giá trị deal theo từng trạng thái | Một query `GROUP BY`, không tính trong code Go |
| FR-DASH-02 | Tỷ lệ thắng (won / (won + lost)) và doanh số đã chốt theo tháng, 6 tháng gần nhất | Tháng không có deal vẫn hiện giá trị 0 |

### 3.5 Import CSV (IMP)

| Mã | Yêu cầu | Tiêu chí chấp nhận |
| --- | --- | --- |
| FR-IMP-01 | Upload CSV (tối đa 20 MB, UTF-8 có hoặc không BOM), tạo `import_job`, trả 202 và job id ngay | Request không chờ xử lý xong |
| FR-IMP-02 | Xử lý bằng worker pool: 1 goroutine đọc file, N worker validate, 1 goroutine ghi theo lô (batch 500 dòng bằng `INSERT ... SELECT FROM unnest(...)`; không dùng `COPY` vì PostgreSQL không hỗ trợ `COPY FROM` vào bảng bật RLS) | N cấu hình được; không load cả file vào RAM |
| FR-IMP-03 | Dòng lỗi bị bỏ qua và ghi lại (số dòng, lý do); dòng hợp lệ vẫn được nhập | Job kết thúc với tổng, thành công, lỗi |
| FR-IMP-04 | API xem tiến độ job (đã xử lý / tổng), tải danh sách dòng lỗi | Frontend poll mỗi 1 giây hoặc dùng SSE |
| FR-IMP-05 | Hủy job đang chạy bằng `context` | Các goroutine dừng sạch, không rò rỉ |

### 3.6 Thành viên và hệ thống (SYS)

| Mã | Yêu cầu |
| --- | --- |
| FR-SYS-01 | `GET /healthz` (sống) và `GET /readyz` (kết nối DB được) |
| FR-SYS-02 | Mọi lỗi trả về cùng định dạng: `{"error":{"code":"...","message":"...","details":...},"request_id":"..."}` |
| FR-SYS-03 | Script seed tạo 2 tenant demo với dữ liệu mẫu và tài khoản dùng thử |

## 4. SRS — Yêu cầu phi chức năng

Mọi yêu cầu dưới đây phải đo được hoặc kiểm chứng được bằng test, công cụ hay checklist.

### 4.1 Cô lập tenant (ưu tiên cao nhất)

| Mã | Yêu cầu | Cách kiểm chứng |
| --- | --- | --- |
| NFR-ISO-01 | Hai lớp bảo vệ: code luôn lọc `tenant_id`, và PostgreSQL Row-Level Security chặn ở tầng DB | Integration test |
| NFR-ISO-02 | Mỗi request chạy trong transaction có `SELECT set_config('app.tenant_id', $1, true)`; policy RLS so `tenant_id` với giá trị này | Test: cố ý viết query thiếu `WHERE tenant_id`, vẫn chỉ thấy dữ liệu tenant của mình |
| NFR-ISO-03 | App kết nối bằng role `app_user` không phải owner, không có `BYPASSRLS`; bảng dùng `FORCE ROW LEVEL SECURITY` | Kiểm tra trong migration |
| NFR-ISO-04 | Truy cập id của tenant khác trả 404 (không phải 403) để không lộ việc bản ghi tồn tại | Integration test |

### 4.2 Hiệu năng

| Mã | Yêu cầu | Cách kiểm chứng |
| --- | --- | --- |
| NFR-PERF-01 | p95 < 100 ms cho API đọc, p95 < 200 ms cho API ghi, ở 200 request/giây với 100.000 khách hàng | k6 trên máy local, kết quả ghi vào README |
| NFR-PERF-02 | Import 100.000 dòng CSV dưới 30 giây (máy local), RAM backend không tăng quá 100 MB | Benchmark + pprof |
| NFR-PERF-03 | Mọi query danh sách có index phù hợp, không Seq Scan trên bảng lớn | `EXPLAIN ANALYZE` ghi trong tài liệu |
| NFR-PERF-04 | Connection pool cấu hình được (mặc định tối đa 10 kết nối cho gói free) | Config |

### 4.3 Bảo mật

| Mã | Yêu cầu |
| --- | --- |
| NFR-SEC-01 | Đối chiếu OWASP API Security Top 10, đặc biệt lỗi BOLA (truy cập object của người khác) |
| NFR-SEC-02 | Rate limit đăng nhập: 5 lần sai / 15 phút / IP + email; API chung: 100 request / phút / user |
| NFR-SEC-03 | Cookie `HttpOnly`, `Secure`, `SameSite=Lax`; frontend và API cùng origin qua Next.js rewrites (xem mục 5) |
| NFR-SEC-04 | Validate mọi input ở biên (binding + validator); chỉ dùng query có tham số, không nối chuỗi SQL |
| NFR-SEC-05 | Secrets chỉ nằm trong biến môi trường; repo có `.env.example`, không có `.env` |
| NFR-SEC-06 | Header bảo mật cơ bản, CORS chỉ cho domain frontend, giới hạn kích thước body |

### 4.4 Độ tin cậy

| Mã | Yêu cầu |
| --- | --- |
| NFR-REL-01 | Graceful shutdown: nhận SIGTERM thì ngừng nhận request mới, chờ request đang chạy tối đa 10 giây |
| NFR-REL-02 | Mọi thao tác nhiều bước nằm trong transaction; lỗi thì rollback toàn bộ |
| NFR-REL-03 | Timeout cho mọi query qua `context` (mặc định 5 giây) |
| NFR-REL-04 | Migration chạy tự động khi deploy, có bản down để rollback |

### 4.5 Khả năng quan sát

| Mã | Yêu cầu |
| --- | --- |
| NFR-OBS-01 | Log có cấu trúc bằng `log/slog` dạng JSON, mỗi dòng có `request_id`, `tenant_id`, `user_id`, độ trễ, mã trạng thái |
| NFR-OBS-02 | Không log mật khẩu, token, cookie |
| NFR-OBS-03 | Endpoint pprof chỉ bật ở môi trường dev |

### 4.6 Chất lượng code

| Mã | Yêu cầu |
| --- | --- |
| NFR-QA-01 | `golangci-lint` và `go vet` sạch; `go test -race` pass trong CI |
| NFR-QA-02 | Unit test cho service (mock repository qua interface), integration test cho repository với Postgres thật (testcontainers) |
| NFR-QA-03 | Coverage tầng service ≥ 70% |
| NFR-QA-04 | API mô tả bằng OpenAPI 3, có trang Swagger UI ở môi trường dev |

### 4.7 Ràng buộc vận hành

- Toàn bộ chạy trên gói miễn phí: backend ngủ sau 15 phút không hoạt động, database 1 GB, nên dữ liệu demo giữ dưới 200 MB.
- Benchmark chạy trên máy local hoặc Docker Compose, không chạy trên môi trường free (kết quả không đại diện và có thể vi phạm điều khoản).

## 5. Kiến trúc hệ thống

Hệ thống là một modular monolith: một backend Go duy nhất, chia module theo nghiệp vụ (auth, contact, deal, dashboard, importer), mỗi module có 3 tầng handler → service → repository. Không dùng microservices ở giai đoạn này; ranh giới module rõ ràng để sau này tách ra nếu cần.

&#91;embedded content: kiến trúc · frontend, backend Gin, PostgreSQL\]

Request đi từ trình duyệt qua Next.js tới Go; mọi truy vấn đều chạy trong `WithTenantTx`, nên PostgreSQL biết tenant hiện tại và RLS chặn dữ liệu của tenant khác.

### 5.1 Luồng một request

1. Trình duyệt gọi `/api/...` trên domain của frontend. Next.js rewrites chuyển tiếp sang backend, nên với trình duyệt đây là cùng origin: cookie httpOnly hoạt động, không cần CORS phức tạp.
2. Middleware Gin: request id → log → recover → rate limit → xác thực JWT (đưa `user_id`, `tenant_id`, `role` vào context).
3. Handler bind và validate input, gọi service.
4. Service mở transaction qua helper `WithTenantTx(ctx, tenantID, fn)`: helper chạy `set_config('app.tenant_id', ...)` rồi mới gọi repository. RLS của PostgreSQL lọc dữ liệu lần thứ hai.
5. Lỗi nghiệp vụ (sentinel error như `ErrNotFound`, `ErrVersionConflict`) được một middleware chung đổi thành mã HTTP và JSON lỗi thống nhất.

### 5.2 Cấu trúc thư mục backend

Tổ chức theo nghiệp vụ (package by feature), không theo loại file. Mở thư mục `deal/` là thấy toàn bộ logic về deal.

```
crm-backend/
├── cmd/api/main.go            # điểm khởi động: đọc config, nối DB, dựng router, graceful shutdown
├── internal/
│   ├── config/                # đọc biến môi trường
│   ├── platform/
│   │   ├── database/          # pgxpool, WithTenantTx
│   │   ├── httpx/             # format lỗi, phân trang cursor, middleware chung
│   │   └── logger/            # slog
│   ├── auth/                  # handler.go, service.go, repository.go, jwt.go, middleware.go
│   ├── contact/
│   ├── deal/                  # có stage.go chứa state machine
│   ├── dashboard/
│   ├── importer/              # worker pool
│   └── server/router.go       # gắn route của các module
├── db/
│   ├── migrations/            # 0001_init.sql ... (goose)
│   ├── queries/               # *.sql cho sqlc
│   └── sqlc/                  # code sinh ra, không sửa tay
├── test/
│   ├── integration/           # testcontainers
│   └── k6/                    # script load test
├── api/openapi.yaml
├── Dockerfile                 # multi-stage, image cuối distroless
├── docker-compose.yml         # postgres local
├── Makefile                   # run, test, lint, migrate, sqlc, seed
└── .golangci.yml
```

### 5.3 Cấu trúc thư mục frontend

```
crm-frontend/
├── src/app/
│   ├── (auth)/login, register
│   └── (app)/dashboard, contacts, contacts/[id], deals, imports, settings/members
├── src/features/              # contacts/, deals/ ...: hooks TanStack Query, form, component riêng
├── src/components/ui/         # shadcn/ui
├── src/lib/api.ts             # fetch client, xử lý 401 → gọi refresh → thử lại
├── src/lib/types.ts           # type sinh từ openapi.yaml (openapi-typescript)
├── proxy.ts                   # (Next.js 16, trước đây là middleware.ts) chưa đăng nhập thì chuyển về /login
└── next.config.ts             # rewrites /api/:path* → BACKEND_URL
```

### 5.4 Mô hình dữ liệu

ID dùng UUID v7 sinh trong Go (có thứ tự theo thời gian, tốt cho index). Tiền lưu `BIGINT` theo VND. Mọi bảng nghiệp vụ có `tenant_id`.

| Bảng | Cột chính | Ghi chú |
| --- | --- | --- |
| tenants | id, name, slug (unique), created\_at | Không bật RLS |
| users | id, tenant\_id, email (citext, unique), password\_hash, full\_name, role (`owner`/`member`), created\_at | RLS; đăng nhập tra cứu qua hàm `SECURITY DEFINER` vì lúc đó chưa biết tenant |
| refresh\_tokens | id, user\_id, tenant\_id, token\_hash (unique), family\_id, expires\_at, revoked\_at | `family_id` phục vụ thu hồi cả chuỗi khi phát hiện dùng lại |
| contacts | id, tenant\_id, full\_name, email, phone, company, notes, owner\_id, created\_at, updated\_at, deleted\_at | Unique (tenant\_id, email) khi chưa xóa |
| deals | id, tenant\_id, contact\_id, title, amount, stage, expected\_close\_date, owner\_id, lost\_reason, version, closed\_at, created\_at, updated\_at | CHECK stage hợp lệ, amount ≥ 0 |
| deal\_stage\_history | id, tenant\_id, deal\_id, from\_stage, to\_stage, changed\_by, changed\_at | Chỉ thêm, không sửa |
| import\_jobs | id, tenant\_id, created\_by, status, total\_rows, processed\_rows, success\_rows, error\_rows, created\_at, finished\_at |  |
| import\_job\_errors | job\_id, tenant\_id, row\_number, reason |  |

Index quan trọng:

- `contacts (tenant_id, created_at DESC, id DESC) WHERE deleted_at IS NULL` cho phân trang cursor.
- GIN `pg_trgm` trên `immutable_unaccent(lower(full_name || ' ' || coalesce(email,'') || ' ' || coalesce(phone,'')))` cho tìm kiếm không dấu. Cần tự tạo hàm `immutable_unaccent` bọc `unaccent`, vì bản gốc không được dùng trong index.
- `deals (tenant_id, stage)` cho dashboard và Kanban.

### 5.5 Row-Level Security

```sql
-- Role ứng dụng: không phải owner của bảng, không có BYPASSRLS
CREATE ROLE app_user LOGIN PASSWORD '...';

ALTER TABLE contacts ENABLE ROW LEVEL SECURITY;
ALTER TABLE contacts FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON contacts
  USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
  WITH CHECK (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
```

```go
// Mỗi request: mở transaction, gắn tenant chỉ trong phạm vi transaction (is_local = true)
func (db *DB) WithTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(q *sqlc.Queries) error) error {
    return pgx.BeginFunc(ctx, db.pool, func(tx pgx.Tx) error {
        if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID.String()); err != nil {
            return err
        }
        return fn(sqlc.New(tx))
    })
}
```

Ba điểm hay bị hỏi khi phỏng vấn:

- `NULLIF(..., '')`: khi chưa gắn tenant, setting trả về rỗng hoặc NULL; so sánh với NULL cho kết quả rỗng, tức mặc định là không thấy gì (fail closed).
- `set_config(..., true)` chỉ có hiệu lực trong transaction, nên kết nối trả về pool không mang tenant cũ sang request khác.
- Owner của bảng mặc định bỏ qua RLS; vì vậy cần `FORCE` và role riêng. Trên Neon, role mặc định là owner, nên phải tạo `app_user`.

### 5.6 Optimistic locking

```sql
-- name: UpdateDeal :one
UPDATE deals
SET title = $3, amount = $4, expected_close_date = $5, version = version + 1, updated_at = now()
WHERE id = $1 AND version = $2
RETURNING *;
```

Không có dòng nào được cập nhật (`pgx.ErrNoRows`) thì service đọc lại deal: không tồn tại trả 404, còn tồn tại trả 409 `VERSION_CONFLICT` kèm bản mới nhất.

### 5.7 API (REST, tiền tố `/api/v1`)

| Method | Path | Mô tả | Yêu cầu |
| --- | --- | --- | --- |
| POST | /auth/register | Tạo tenant + owner | FR-AUTH-01 |
| POST | /auth/login | Đăng nhập, set cookie | FR-AUTH-03 |
| POST | /auth/refresh | Xoay vòng refresh token | FR-AUTH-04 |
| POST | /auth/logout | Thu hồi token | FR-AUTH-05 |
| GET | /me | Thông tin user và tenant hiện tại | FR-AUTH-06 |
| GET, POST | /members | Danh sách, thêm thành viên (owner) | FR-AUTH-07 |
| GET, POST | /contacts | Danh sách (`?q=&cursor=&limit=`), tạo mới | FR-CON-01..03 |
| GET, PATCH, DELETE | /contacts/{id} | Chi tiết, sửa, xóa mềm | FR-CON-04 |
| GET, POST | /deals | Danh sách (`?stage=&owner_id=&contact_id=`), tạo mới | FR-DEAL-01, 05 |
| GET, PATCH | /deals/{id} | Chi tiết, sửa (body có `version`) | FR-DEAL-04 |
| POST | /deals/{id}/transition | `{to_stage, version, lost_reason?}` | FR-DEAL-02, 03 |
| GET | /dashboard/pipeline | Số lượng, tổng giá trị theo stage | FR-DASH-01 |
| GET | /dashboard/monthly | Doanh số, tỷ lệ thắng 6 tháng | FR-DASH-02 |
| POST | /imports/contacts | Upload CSV, trả 202 + job id | FR-IMP-01 |
| GET | /imports/{id} | Tiến độ job | FR-IMP-04 |
| GET | /imports/{id}/errors | Danh sách dòng lỗi | FR-IMP-03 |
| POST | /imports/{id}/cancel | Hủy job | FR-IMP-05 |
| GET | /healthz, /readyz | Kiểm tra sức khỏe (ngoài `/api/v1`) | FR-SYS-01 |

## 6. Deploy miễn phí và CI/CD

Bộ đề xuất: Vercel cho frontend, Render cho backend, Neon cho PostgreSQL; tất cả không cần thẻ thanh toán. Thông tin gói free kiểm tra tháng 11/2026 và hay thay đổi, nên xem lại trước khi deploy.

| Thành phần | Dịch vụ | Giới hạn gói free cần biết | Cách xử lý |
| --- | --- | --- | --- |
| Frontend Next.js | Vercel (Hobby) | Dành cho dự án cá nhân, phi thương mại | Phù hợp project portfolio |
| Backend Go | Render (Free web service, Docker) | Ngủ sau 15 phút không có request; băng thông free 5 GB/tháng | Go khởi động rất nhanh, nhưng lần gọi đầu vẫn chờ vài chục giây để máy thức dậy; ghi rõ trong README |
| PostgreSQL | Neon (Free) | 1 GB mỗi project, 100 CU-giờ/tháng, tự ngắt khi rảnh nên query đầu tiên chậm hơn | Giữ dữ liệu demo nhỏ; chọn region Singapore |
| Phương án thay thế backend | Google Cloud Run | Free 2 triệu request/tháng nhưng cần thẻ | Dùng nếu muốn có chữ “GCP” trên CV |

Lưu ý: Fly.io không còn gói miễn phí cho tài khoản mới từ tháng 10/2024, nên không dùng.

### Cấu hình quan trọng

- **Hai chuỗi kết nối Neon:** migration dùng role owner và kết nối trực tiếp (không qua pooler); ứng dụng dùng role `app_user`. Nếu dùng endpoint pooled của Neon (PgBouncer chế độ transaction) thì cấu hình pgx `default_query_exec_mode=simple_protocol` để tránh lỗi prepared statement.
- **Next.js rewrites:** `/api/:path*` → `${BACKEND_URL}/api/:path*`. Trình duyệt chỉ nói chuyện với domain Vercel, cookie là first-party.
- **Biến môi trường backend:** `DATABASE_URL`, `MIGRATION_DATABASE_URL`, `JWT_SECRET`, `FRONTEND_ORIGIN`, `APP_ENV`, `PORT` (Render cấp).
- **Dockerfile multi-stage:** stage build `golang:1.27`, stage chạy `gcr.io/distroless/static`; build với `CGO_ENABLED=0`. Image cuối khoảng 20 MB.

### Pipeline GitHub Actions

1. Pull request: `golangci-lint` → `go test -race ./...` (integration test dùng Postgres service container) → `sqlc diff` (code sinh ra khớp với SQL) → build Docker.
2. Merge vào `main`: chạy migration lên Neon → gọi deploy hook của Render.
3. Frontend: Vercel tự deploy khi push; mỗi pull request có bản preview.
4. Pull request frontend: `tsc --noEmit`, ESLint, test component (Vitest), build.

### Nguồn

- [State of Free Hosting 2026 – SnapDeploy](https://snapdeploy.dev/state-of-free-hosting)
- [Neon Free plan limits](https://neon.com/faqs/free-plan-limits-and-quotas)
- [Free Go stack – AgentDeals](https://agentdeals.dev/free-go-stack)

## 7. Kế hoạch triển khai (MVP, khoảng 8 tuần bán thời gian)

Backend làm trước và xong hẳn phần lõi, rồi mới dựng frontend. Cuối mỗi tuần phải có thứ chạy được và commit lên GitHub. Tick vào ô khi xong.

### Tuần 0 — Học nền tảng Go (5–7 ngày)

- [ ] Cài Go, Cursor extension, Docker (xem tài liệu học Go)
- [ ] Hoàn thành A Tour of Go: cú pháp, struct, interface, error, goroutine, channel
- [ ] Viết 1 API nhỏ bằng Gin không dùng DB (CRUD trên map có `sync.RWMutex`) để quen handler, binding, test

### Tuần 1 — Khung dự án và database

- [ ] Khởi tạo repo, `go mod init`, cấu trúc thư mục, Makefile, docker-compose Postgres
- [ ] Config từ env, slog, graceful shutdown, `/healthz`, `/readyz`
- [ ] Migration đầu tiên (tenants, users, refresh\_tokens), cấu hình sqlc
- [ ] GitHub Actions: lint + test

### Tuần 2 — Xác thực và cô lập tenant

- [ ] FR-AUTH-01..06: đăng ký, đăng nhập, refresh rotation, đăng xuất, middleware JWT
- [ ] RLS + `app_user` + `WithTenantTx` (NFR-ISO-01..03)
- [ ] Integration test với testcontainers: tenant A không đọc được dữ liệu tenant B, kể cả query thiếu `WHERE`

### Tuần 3 — Khách hàng

- [ ] FR-CON-01..04: CRUD, xóa mềm, phân trang cursor
- [ ] Tìm kiếm không dấu (`unaccent` + `pg_trgm`), kiểm tra bằng `EXPLAIN ANALYZE`
- [ ] Format lỗi thống nhất, rate limit

### Tuần 4 — Deal, pipeline, dashboard

- [ ] State machine `stage.go` + unit test bảng (table-driven test) cho mọi cặp chuyển trạng thái
- [ ] Lịch sử chuyển trạng thái trong cùng transaction
- [ ] Optimistic locking + test 2 goroutine sửa cùng lúc (chạy với `-race`)
- [ ] API dashboard (FR-DASH-01, 02)

### Tuần 5 — Import CSV và hiệu năng

- [ ] Worker pool: reader → N validator → batch writer, hủy bằng context
- [ ] Script sinh file 100.000 dòng, đo thời gian và RAM (pprof)
- [ ] Seed 100.000 khách, chạy k6, ghi p95 vào README
- [ ] OpenAPI spec hoàn chỉnh

### Tuần 6 — Deploy backend

- [ ] Dockerfile multi-stage, deploy Render, database Neon, migration trong CI
- [ ] Script seed dữ liệu demo (FR-SYS-03)

### Tuần 7 — Frontend

- [ ] Khởi tạo Next.js 16 + Tailwind + shadcn/ui, rewrites tới backend, sinh type từ OpenAPI
- [ ] Trang đăng ký, đăng nhập, middleware bảo vệ route
- [ ] Danh sách và form khách hàng, tìm kiếm có debounce
- [ ] Kanban deal (kéo thả), xử lý lỗi 409 bằng hộp thoại “người khác vừa sửa”

### Tuần 8 — Hoàn thiện

- [ ] Dashboard có biểu đồ, trang import có thanh tiến độ
- [ ] Deploy Vercel, kiểm tra toàn bộ luồng trên môi trường thật
- [ ] README: sơ đồ kiến trúc, link demo, tài khoản dùng thử, số liệu benchmark, giải thích 3 bài toán khó
- [ ] Quay video demo 2 phút, cập nhật CV

### Câu mô tả trên CV (điền số thật khi đo xong)

> Mini CRM multi-tenant (Go, Gin, PostgreSQL, Next.js): cô lập dữ liệu tenant bằng PostgreSQL Row-Level Security; import 100.000 khách hàng trong X giây bằng worker pool; chống ghi đè bằng optimistic locking; p95 Y ms ở 200 req/s; CI/CD với GitHub Actions.

## 8. Lộ trình mở rộng thành hệ thống doanh nghiệp

Sau MVP, hệ thống lớn dần qua 4 giai đoạn nữa; chỉ sang giai đoạn sau khi giai đoạn trước đạt điều kiện chuyển tiếp. Mỗi giai đoạn gắn với một nhóm kiến thức bạn sẽ học thêm.

&#91;embedded content: lộ trình · 5 giai đoạn, 4 cổng kiểm tra\]

Hình thoi trên trục là cổng kiểm tra: đạt điều kiện ghi bên dưới mới sang giai đoạn kế tiếp.

### Giai đoạn 2 — SaaS cho đội nhóm thật

**Học thêm:** phân quyền, background job, gửi email, lưu trữ file.

- RBAC: vai trò Owner, Admin, Manager, Sales, Viewer; quyền theo hành động (`contact:read`, `deal:update`...), sale chỉ thấy deal của mình hoặc của nhóm.
- Mời thành viên qua email, xác minh email, quên mật khẩu.
- Hoạt động: ghi chú, cuộc gọi, công việc có hạn, nhắc việc; timeline hoạt động của mỗi khách.
- File đính kèm qua presigned URL (Cloudflare R2, tương thích S3).
- Background job bằng River (hàng đợi chạy trên PostgreSQL), chuyển import CSV sang job bền vững.
- Audit log: ai làm gì, lúc nào, giá trị trước và sau.

**Điều kiện chuyển tiếp:** một đội 5 người dùng thật (bạn bè, câu lạc bộ) trong 2 tuần không gặp lỗi phân quyền.

### Giai đoạn 3 — Độ tin cậy và khả năng mở rộng

**Học thêm:** kiến trúc hướng sự kiện, observability, caching.

- Transactional outbox: sự kiện nghiệp vụ (`deal.won`, `contact.created`) ghi cùng transaction, worker phát đi sau.
- Webhook ra ngoài có ký HMAC, retry với exponential backoff.
- OpenTelemetry: trace xuyên frontend → backend → database, đẩy về Grafana Cloud.
- Redis cho cache dashboard và rate limit phân tán khi chạy nhiều instance.
- Partition bảng hoạt động và audit log theo tháng; full-text search tiếng Việt tốt hơn.
- Rate limit và quota theo tenant (chống “hàng xóm ồn ào” — noisy neighbor).

**Điều kiện chuyển tiếp:** chạy 2 instance backend song song mà không sai dữ liệu; load test 1.000 req/s trên máy local với p95 đạt mục tiêu.

### Giai đoạn 4 — Tính năng doanh nghiệp

**Học thêm:** bảo mật doanh nghiệp, thanh toán, thiết kế dữ liệu linh hoạt.

- SSO qua OIDC (Google Workspace, Microsoft Entra), xác thực hai lớp.
- Trường tùy biến (JSONB + JSON Schema) và pipeline tùy biến theo tenant.
- Tự động hóa quy trình: “khi deal sang Won thì tạo công việc và gửi email”.
- Gói dịch vụ và giới hạn theo gói (số user, số khách), tích hợp cổng thanh toán ở chế độ test.
- Xuất và xóa toàn bộ dữ liệu của tenant theo yêu cầu (phục vụ quy định bảo vệ dữ liệu cá nhân).
- Đa ngôn ngữ (Việt, Anh), múi giờ và đơn vị tiền theo tenant.

**Điều kiện chuyển tiếp:** một tenant cấu hình được pipeline, trường và quy trình riêng mà không cần sửa code.

### Giai đoạn 5 — Nền tảng ERP thu nhỏ

**Học thêm:** thiết kế hệ thống phân tán, hạ tầng.

- Module mới trong cùng monolith: báo giá và đơn hàng, sản phẩm và tồn kho, nhân sự cơ bản.
- Public API có API key và tài liệu cho đối tác tích hợp.
- Tách module chịu tải riêng (ví dụ importer, notification) thành service khi có số liệu chứng minh cần tách; giao tiếp qua message queue.
- Hạ tầng: Kubernetes, Terraform, môi trường staging, backup và diễn tập khôi phục.
- Tính năng AI: chấm điểm lead, tóm tắt lịch sử khách, soạn email theo ngữ cảnh. Đây là nơi ý tưởng LLM Gateway trước đó có thể gắn vào.

### Nguyên tắc khi mở rộng

- Không tách microservice khi chưa có số liệu. Ranh giới module sạch quan trọng hơn số lượng service.
- Mỗi tính năng mới phải giữ được test cô lập tenant.
- Chi phí: giai đoạn 2 vẫn chạy được trên gói free; từ giai đoạn 3 có thể cần vài đô mỗi tháng hoặc chạy local bằng Docker Compose để học.
