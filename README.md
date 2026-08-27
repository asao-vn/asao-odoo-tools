# 🏢 ASAO Odoo 19 Conformance & AERP Integration Toolkit

Bộ công cụ kiểm thử chuẩn tương thích và tạo token xác thực cho hệ sinh thái **Odoo 19 & ASAO AERP (Autonomous Enterprise Resource Planning)**.

---

## 📦 Các Công Cụ Trong Bộ Toolkit

### 1. `odoo19-conformance`
Công cụ chạy kiểm thử tự động toàn diện để xác minh phiên bản Odoo 19 đáp ứng đầy đủ các tiêu chuẩn RPC, Data Schema, Concurrency Lock và Semantic Contracts của ASAO.

```bash
# Chạy conformance test kiểm tra Odoo 19 instance:
./bin/odoo19-conformance \
  --org-id="<ORG_ID>" \
  --db="<POSTGRES_DB>" \
  --grpc-addr="127.0.0.1:9082"
```

### 2. `minttoken`
Tiện ích tạo và ký số HMAC-SHA256 bearer tokens cho kết nối an toàn giữa Odoo bridge và ASAO Core:

```bash
./bin/minttoken \
  --service="organization-service" \
  --secret="<HMAC_SECRET_KEY>"
```

### 3. `probetoken`
Tiện ích kiểm tra tính hợp lệ của token và kiểm tra độ trễ (latency/skew) của Odoo RPC gateway.

---

## 🛠️ Biên Dịch từ Source Code

```bash
# Yêu cầu Go >= 1.22
git clone https://github.com/NguyenHuuHoang711/asao-odoo-tools.git
cd asao-odoo-tools

make build
# Binary sẽ nằm trong thư mục bin/
```

---

## 📄 Bản Quyền
Bản quyền thuộc về ASAO Sovereign Cloud Architecture. Giấy phép mã nguồn mở Apache 2.0 / MIT.
