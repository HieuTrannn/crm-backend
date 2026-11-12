# CLAUDE.md — crm-backend

## Bối cảnh
Mini CRM SaaS multi-tenant. Yêu cầu chi tiết ở docs/PRD_SRS.md (mã FR-*, NFR-*).
Người phát triển đang HỌC Go (đã biết C#/.NET, EF Core). Ưu tiên code dễ hiểu, idiomatic,
không “thông minh” quá mức. Luôn giải thích bằng tiếng Việt; tên biến, comment code, commit bằng tiếng Anh.

## Stack (không tự ý thay đổi)
- Go 1.27, Gin, pgx v5 (pgxpool), sqlc, goose, PostgreSQL 17
- log/slog (JSON), golang-jwt/jwt/v5, golang.org/x/crypto/bcrypt, google/uuid (UUID v7)
- Test: testing, testify, testcontainers-go. Lint: golangci-lint
- KHÔNG dùng ORM (GORM...), KHÔNG dùng framework DI. Wire dependency bằng tay trong cmd/api/main.go.

## Cấu trúc
- cmd/api/main.go: khởi động, wiring, graceful shutdown
- internal/<feature>/ (auth, contact, deal, dashboard, importer): handler.go, service.go, repository.go, model.go, errors.go
- internal/platform/{database,httpx,logger}: hạ tầng dùng chung
- db/migrations (goose, SQL thuần), db/queries (sqlc), db/sqlc (code sinh, KHÔNG sửa tay)

## Quy tắc kiến trúc
- handler: bind + validate input, gọi service, map kết quả ra JSON. Không chứa logic nghiệp vụ, không gọi DB.
- service: logic nghiệp vụ, transaction. Phụ thuộc vào interface repository khai báo ở PHÍA service (“accept interfaces”).
- repository: bọc sqlc, không chứa logic nghiệp vụ.
- Mọi hàm nhận context.Context làm tham số đầu tiên.
- Lỗi nghiệp vụ là sentinel error trong errors.go (ErrNotFound, ErrConflict, ErrVersionConflict, ErrInvalidTransition...).
  Bọc lỗi bằng fmt.Errorf("...: %w", err); kiểm tra bằng errors.Is / errors.As. Middleware httpx map lỗi → HTTP.
- Không panic trong code nghiệp vụ. Không dùng biến global (trừ hằng số).

## Multi-tenant (BẮT BUỘC, không ngoại lệ)
- Mọi truy vấn dữ liệu nghiệp vụ chạy trong database.WithTenantTx(ctx, tenantID, fn).
- Mọi query sqlc vẫn có điều kiện tenant_id (lớp 1), RLS là lớp 2.
- Bảng nghiệp vụ mới: có cột tenant_id, ENABLE + FORCE ROW LEVEL SECURITY, policy tenant_isolation, GRANT cho app_user.
- Truy cập bản ghi của tenant khác trả 404.
- Mọi tính năng mới phải có integration test chứng minh cô lập tenant.

## Quy ước API
- Tiền tố /api/v1, JSON snake_case, thời gian RFC 3339 UTC, tiền là số nguyên VND.
- Lỗi: {"error":{"code":"UPPER_SNAKE","message":"...","details":...},"request_id":"..."}
- Phân trang cursor: ?limit=&cursor= → {"data":[...],"next_cursor":"..."|null}
- Cập nhật API thì cập nhật api/openapi.yaml trong cùng thay đổi.

## Bảo mật
- Không log mật khẩu, token, cookie, chuỗi kết nối.
- Chỉ dùng query có tham số. Không nối chuỗi SQL.
- Secret chỉ đọc từ biến môi trường; cập nhật .env.example khi thêm biến.

## Lệnh
- make run | make test | make test-integration | make lint | make migrate-up | make sqlc | make seed
- Trước khi báo “xong”: make sqlc && make lint && make test phải pass. go test dùng -race.

## Cách làm việc (chế độ học)
- Mỗi lần chỉ làm đúng phạm vi được yêu cầu. Không tự thêm tính năng.
- Trước khi code: nêu kế hoạch ngắn (file sẽ tạo/sửa, vì sao).
- Sau khi code: viết mục “Giải thích cho người học” gồm:
  1. Khái niệm Go mới xuất hiện (ví dụ defer, interface ngầm, goroutine) và so sánh với C# nếu có ích.
  2. Quyết định thiết kế và phương án đã loại.
  3. 2–3 câu hỏi phỏng vấn có thể gặp về phần này.
- Nếu yêu cầu mơ hồ hoặc mâu thuẫn với docs/PRD_SRS.md: hỏi lại, không đoán.