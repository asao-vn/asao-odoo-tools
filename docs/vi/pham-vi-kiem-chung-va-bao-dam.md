# Odoo: ASAO kiểm chứng gì và không bảo đảm gì

Ngày 11/10/2026. Quyết định liên quan: DEC-2026-0157.

> **ASAO chỉ kiểm chứng từng thao tác trên bản Odoo chuẩn, tức image gốc của mỗi phiên bản.** Odoo đã
> tuỳ biến ngoài bản chuẩn là Odoo có module Enterprise, module bên thứ ba hoặc tự viết, tuỳ biến bằng
> Studio, luật tự động, hay trường tự thêm. Với Odoo như vậy, ASAO không thể kiểm chứng và không bảo
> đảm thao tác chạy đúng như mô tả hay không có sai sót. Thao tác được chấp nhận chạy trên Odoo đó là
> do người dùng tự chịu rủi ro.

## ASAO kiểm chứng gì

- **Phiên bản:**
  - Odoo 19.0, bản Community chuẩn: đã kiểm chứng.
  - Odoo 20.0: mới là ứng viên, chưa có bằng chứng, chỉ chạy khi người dùng chấp nhận.
  - Các bản khác đều bị từ chối: 18 trở xuống, bản trung gian của Odoo Online (`saas~`), bản mới hơn.
- **Từng thao tác:** mỗi thao tác mang mức xác minh của nó (`CURRENT_VERIFIED`, `UNVERIFIED`,
  `HISTORICAL_ONLY`). Hiện 696 trên 832 thao tác đã xác minh trên 19.0.
- **Cách kiểm:** trên lab chạy bản chuẩn, so kết quả của CLI với chính Odoo:
  - lệnh đọc so với dữ liệu đọc thẳng từ Odoo;
  - lệnh ghi so với bản ghi đọc lại sau khi ghi;
  - lệnh in so với file PDF trình duyệt tải về.

## Odoo đã tuỳ biến: không kiểm chứng, không bảo đảm

Các trường hợp dưới đây đều tính là tuỳ biến:
- Module Enterprise. Bằng chứng của ASAO được ghi trên bản Community.
- Module bên thứ ba, như OCA hay module của đối tác, và module tự viết.
- Tuỳ biến bằng Studio, trường tự thêm, luật tự động.
- Sửa thẳng mã nguồn Odoo.

Những thay đổi này có thể làm một thao tác ghi khác đi hoặc gây ra việc khác: gửi email, đổi giá trị,
chặn lưu. ASAO không có cách kiểm chứng chúng thay cho người dùng.

## CLI cảnh báo thế nào

Trước mỗi thao tác, CLI `odoo-cli` (và SDK Python khi chạy theo profile) kiểm tra Odoo mà nó kết nối:
- có phải Odoo không, và là phiên bản nào;
- Community hay Enterprise;
- có module nào ngoài image chuẩn không;
- trên model mà thao tác sắp ghi: có module nào mở rộng nó, có luật tự động hay trường bắt buộc tự thêm
  không.

| Tình huống | CLI làm gì |
|---|---|
| Không phải Odoo, hoặc phiên bản không hỗ trợ | Từ chối, chưa gửi gì đi, kể cả mật khẩu |
| Thiếu module mà thao tác cần | Từ chối trước khi gửi (`AERP_MODULE_NOT_INSTALLED`) |
| Ghi vào chỗ Odoo đã tuỳ biến | Giữ lại (`AERP_ENVIRONMENT_UNVERIFIED`). Lỗi nêu lý do kèm thông báo ở đầu tài liệu này |
| Người dùng vẫn muốn chạy | Thêm `--allow-unverified`, hoặc `Client(..., allow_unverified=True)` trong SDK. Kết quả mang `providerFit` với `verdict: UNVERIFIED_HERE`, lý do và thông báo |
| Đọc trên Odoo đã tuỳ biến | Vẫn chạy. Nếu Odoo có module ngoài image chuẩn, kết quả kèm ghi chú và thông báo |

`odoo-cli doctor --probe` cho xem toàn bộ: phiên bản, bản Community hay Enterprise, module ngoài, model đã
bị đổi, và số thao tác chạy được, bị giữ lại hay bị từ chối. Với Odoo không còn là bản chuẩn, báo cáo
cũng mang thông báo trên.

## Những gì CLI không phát hiện được

- Mã nguồn Odoo bị sửa thẳng trên máy chủ.
- Phân quyền và luật bản ghi. Những thứ này hiện ra thành lỗi `ACCESS_DENIED` khi chạy.
- Thay đổi giao diện (view). Thao tác đi qua API, không qua giao diện.
- Odoo chạy ở đâu. Từ bên ngoài không chứng minh được; chỉ Odoo do ASAO tự chạy mới đối chiếu được
  digest của image.

Vì vậy, việc CLI không cảnh báo không có nghĩa là Odoo chắc chắn là bản chuẩn.

## Đề xuất bổ sung Điều khoản sử dụng (bản nháp)

**Chỗ đặt:** Mục 9 "Ứng dụng, tích hợp và dịch vụ của bên thứ ba", ngay sau đoạn nói dịch vụ bên thứ
ba chịu điều khoản của chính bên đó. Mục 16 "Miễn trừ bảo đảm" có thể dẫn chiếu sang.

> Đối với Odoo và các Ứng dụng mã nguồn mở được quản lý khác, ${E.brand} chỉ kiểm chứng các thao tác
> trên phiên bản chuẩn của phần mềm đó, theo danh sách phiên bản được công bố. Nếu hệ thống bạn kết nối
> đã được tuỳ biến ngoài phiên bản chuẩn, ${E.brand} không thể kiểm chứng và không bảo đảm rằng các
> thao tác sẽ hoạt động đúng như mô tả hoặc không có sai sót. Tuỳ biến ở đây gồm: cài thêm module
> Enterprise, module của bên thứ ba hoặc do bạn tự phát triển, tuỳ biến bằng Studio, thêm trường dữ
> liệu hay luật tự động. Dịch vụ sẽ cảnh báo khi phát hiện tuỳ biến. Nếu bạn vẫn chọn thực hiện thao
> tác trên hệ thống đã tuỳ biến, bạn tự chịu rủi ro về kết quả của thao tác đó.

File Điều khoản (`apps/asao-web/lib/legal/terms.ts`) đang được một phiên làm việc khác sửa, nên đoạn
này chưa được đưa vào. Điều khoản dự kiến có hiệu lực từ 20/10/2026, nên cần đưa vào trước ngày đó.
