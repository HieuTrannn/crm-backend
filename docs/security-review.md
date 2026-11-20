# Rà soát bảo mật theo OWASP API Security Top 10 (2023)

Ngày rà soát: 2026-11-17. Phạm vi: toàn bộ API trong `internal/` và `cmd/`, migration, cấu hình
Docker/CI. Mỗi mục ghi: rủi ro trong dự án này, cách kiểm chứng, trạng thái.

**Trạng thái:** ✅ đã đạt từ trước · 🔧 sửa trong lần rà soát này · ⚠️ chấp nhận rủi ro hoặc để sau (có lý do).

## Tóm tắt

| # | Hạng mục OWASP | Kết quả |
| --- | --- | --- |
| API1 | Broken Object Level Authorization (BOLA) | ✅ RLS + lọc `tenant_id` + 404 cho bản ghi tenant khác, có integration test cho mọi module |
| API2 | Broken Authentication | ✅ bcrypt cost 12, JWT HS256 cố định thuật toán, refresh token xoay vòng; ⚠️ IP sau proxy |
| API3 | Broken Object Property Level Authorization | ✅ DTO request riêng, không mass assignment; response không lộ cột nội bộ |
| API4 | Unrestricted Resource Consumption | 🔧 rate limit theo user, giới hạn body, timeout server, giới hạn import đồng thời |
| API5 | Broken Function Level Authorization | ✅ chưa có endpoint phân quyền theo vai trò; ⚠️ phải kiểm tra `owner` khi làm FR-AUTH-07 |
| API6 | Unrestricted Access to Sensitive Business Flows | 🔧 giới hạn đăng ký theo IP; ⚠️ 409 `EMAIL_TAKEN` lộ email đã đăng ký (PRD yêu cầu) |
| API7 | Server Side Request Forgery | ✅ API không gọi ra URL nào do người dùng cung cấp |
| API8 | Security Misconfiguration | 🔧 security headers, CORS chỉ `FRONTEND_ORIGIN`, vá lỗ hổng `quic-go`, `govulncheck` trong CI |
| API9 | Improper Inventory Management | 🔧 `api/openapi.yaml` đủ 25 endpoint, hợp lệ (Redocly); Swagger UI chỉ ở development |
| API10 | Unsafe Consumption of APIs | ✅ không gọi API bên thứ ba |

## Chi tiết

### API1 — BOLA

- **Hai lớp:** mọi query sqlc có `WHERE tenant_id = $n`, và RLS (`ENABLE` + `FORCE`) trên mọi bảng nghiệp vụ
  (`users`, `refresh_tokens`, `contacts`, `deals`, `deal_stage_history`, `import_jobs`, `import_job_errors`).
  App kết nối bằng `app_user`, không phải owner, không có `BYPASSRLS`.
- **Khóa ngoại kép** `(tenant_id, x_id)` chặn tham chiếu chéo tenant (owner, contact của deal, user của refresh token),
  vì kiểm tra khóa ngoại bỏ qua RLS.
- **Id của tenant khác trả 404** như bản ghi không tồn tại; id sai định dạng cũng 404.
- **Kiểm chứng:** `TestTenantIsolation`, `TestTenantIsolationAPI`, `TestContactTenantIsolation`,
  `TestDealListAndIsolation`, `TestImportFailuresAndIsolation`, bảng `TestDashboard*` với dữ liệu tenant B.
- Trong cùng tenant, member được sửa/xóa mọi contact và deal: đúng thiết kế PRD (chưa có phân quyền theo người phụ trách).

### API2 — Broken Authentication

- ✅ Mật khẩu bcrypt cost 12, so sánh constant-time; email không tồn tại vẫn chạy bcrypt với hash giả (không dò được email qua thời gian phản hồi).
- ✅ Access JWT 15 phút, `WithValidMethods(HS256)` chặn `alg: none` và nhầm thuật toán; `exp` bắt buộc.
- ✅ Refresh token 32 byte `crypto/rand`, DB chỉ lưu SHA-256; dùng lại token đã thu hồi → thu hồi cả chuỗi.
- ✅ Cookie `HttpOnly`, `SameSite=Lax`, `Secure` ở production; refresh cookie chỉ gửi tới `/api/v1/auth`.
- ✅ Đăng nhập sai 5 lần / 15 phút / IP + email → 429.
- ⚠️ **IP phía sau proxy** (`internal/server/router.go`, ghi chú `shortcut:`): Gin đang không tin proxy nào, nên sau Render/Next.js
  mọi người dùng chung một IP. Giới hạn đăng nhập/đăng ký theo IP khi đó trở thành giới hạn chung. **Phải** cấu hình
  `SetTrustedProxies` với dải IP của Render trước khi deploy.

### API3 — Object Property Level Authorization

- ✅ Mỗi endpoint bind vào struct request riêng (`createRequest`, `updateRequest`...): không thể gửi thêm `tenant_id`, `version`,
  `stage`, `deleted_at`, `role`... để ghi đè. Đổi stage chỉ qua `/transition` (có kiểm tra state machine).
- ✅ Response chỉ chứa trường công khai: không trả `password_hash`, `tenant_id`, `search_text`, `deleted_at`.
- ✅ `owner_id`, `contact_id` do client gửi được kiểm tra thuộc cùng tenant (khóa ngoại kép) → 422.

### API4 — Unrestricted Resource Consumption

| Phát hiện | Rủi ro | Sửa |
| --- | --- | --- |
| Không có rate limit chung | Một user gửi vô hạn request | 🔧 100 request/phút/user trong `auth.RequireAuth` → 429 `RATE_LIMITED` (NFR-SEC-02) |
| Không giới hạn kích thước body JSON | Body vài trăm MB chiếm RAM | 🔧 `httpx.LimitBody`: 1 MB cho JSON; upload CSV có giới hạn riêng 20 MB |
| `http.Server` chỉ có `ReadHeaderTimeout` | Client gửi body rất chậm giữ kết nối mãi | 🔧 `ReadTimeout` 2 phút, `IdleTimeout` 2 phút |
| Không giới hạn số import chạy cùng lúc | Một tenant chiếm hết CPU và kết nối DB | 🔧 tối đa 2 job/tenant → 429 `TOO_MANY_IMPORTS` |
| Phân trang | Trả hàng triệu dòng một lần | ✅ `limit` tối đa 100 |
| Tìm kiếm `LIKE '%q%'` | Ký tự đại diện do người dùng gửi | ✅ `%`, `_`, `\` được escape |

⚠️ Rate limiter và JobManager nằm trong bộ nhớ một process (`shortcut:`): chạy nhiều instance cần Redis/hàng đợi trong DB.

### API5 — Function Level Authorization

- ✅ Hiện chưa có endpoint chỉ dành cho `owner`. Vai trò nằm trong JWT (`role`) và `Principal`.
- ⚠️ Khi làm FR-AUTH-07 (`/members`), phải kiểm tra `p.Role == owner` ở service và có test "member gọi → 403".

### API6 — Sensitive Business Flows

- 🔧 **Đăng ký không giới hạn:** script tạo hàng loạt tenant/tài khoản. Sửa: 10 lần/giờ/IP → 429.
- ⚠️ **Dò email qua đăng ký:** `POST /auth/register` trả 409 `EMAIL_TAKEN` khi email đã có (FR-AUTH-01 yêu cầu).
  Giảm thiểu bằng giới hạn trên. Muốn bỏ hẳn phải đổi luồng (luôn trả 202 và gửi email xác nhận), cần sửa PRD.
- ✅ Import CSV: tối đa 20 MB và 2 job/tenant.

### API7 — SSRF

✅ Không có tính năng nào tải URL do người dùng cung cấp (import nhận file upload, không nhận URL).

### API8 — Security Misconfiguration

| Phát hiện | Sửa |
| --- | --- |
| Thiếu security headers | 🔧 `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`, CSP `default-src 'none'` (riêng `/docs` nới cho CDN), `Cache-Control: no-store`, HSTS ở production |
| CORS chưa cấu hình | 🔧 chỉ `FRONTEND_ORIGIN` được phép, kèm credentials; origin khác không nhận header CORS |
| `quic-go` v0.59.0 có lỗ hổng GO-2026-5676 (đi vào qua Gin), code có đường gọi tới | 🔧 nâng lên v0.59.1; `govulncheck` báo sạch |
| Không quét lỗ hổng tự động | 🔧 thêm `govulncheck` vào job lint của CI |
| Lỗi 500 lộ chi tiết | ✅ trả `INTERNAL_ERROR` chung, chi tiết chỉ ghi log; `failure_reason` của import cũng vậy |
| Log lộ bí mật | ✅ chỉ ghi path (không query string), không ghi body, cookie, mật khẩu |
| Swagger UI ở production | ✅ chỉ mount khi `APP_ENV=development` |
| Image container | ✅ distroless, không shell, chạy user `nonroot` |

### API9 — Inventory Management

- 🔧 `api/openapi.yaml` (3.1) mô tả đủ 25 thao tác, có `operationId`, `security` cho từng endpoint, mã lỗi 400/401/404/409/413/422/429.
  Kiểm tra bằng `redocly lint`: hợp lệ, còn 4 cảnh báo có chủ đích (`/healthz`, `/readyz`, `/logout` không có lỗi 4xx; chưa khai báo license).
- ✅ Chỉ một phiên bản API (`/api/v1`), không có endpoint cũ còn chạy.

### API10 — Unsafe Consumption of APIs

✅ Backend không gọi API bên thứ ba.

## Việc còn lại trước khi deploy

1. Cấu hình trusted proxies cho Render (API2, API4).
2. Kiểm tra vai trò `owner` khi làm `/members` (API5).
3. Nếu chạy nhiều instance: chuyển rate limiter và job import sang Redis/DB (API4).
