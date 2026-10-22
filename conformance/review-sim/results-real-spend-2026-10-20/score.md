| Ca | Phép kiểm tra | Loại | Kết quả | Mức | Mô tả |
|---|---|---|---|---|---|
| rent-same-amount | `O01` | mồi nhử | **KHÔNG BÁO NHẦM** | — | 15 triệu mỗi tháng, số tham chiếu khác nhau: hợp lệ, không được báo |
| bill-twice-same-ref | `O01` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Cùng NCC, cùng số HĐ HD0001234, cùng số tiền, cả hai đã trả: trả tiền hai lần |
| bill-twice-retyped-ref | `O01` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Số HĐ gõ lại có gạch nối (HD0045871 / HD-0045871), cùng số tiền, cả hai đã trả |
| customer-paid-twice | `O22` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Thu tiền khách 2 lần cho cùng một hoá đơn, lần sau chưa đối soát |
| vendor-bank-is-employee | `O17` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | TK ngân hàng NCC trùng TK của thủ kho Trần Văn Bình |
| new-vendor-no-tax-code-paid-fast | `O18` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | NCC mới, không MST, không địa chỉ, nhận 82,7 triệu trong 10 tuần, trả trong 1 ngày |
| supplier-twin-same-tax-code | `O13` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY | Hai hồ sơ NCC cùng MST 9900000102 |
| customer-twin-same-phone | `O13` | cài sẵn | **BẮT ĐƯỢC** | THEO_DOI | Hai hồ sơ khách cùng số điện thoại |
| split-under-approval | `O16` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | 3 đơn mua thép cùng NCC trong 3 ngày, mỗi đơn 19,7 triệu (gồm VAT) < ngưỡng duyệt 20 triệu, tổng 59 triệu |
| below-cost-one-seller | `O19` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Tuấn bán máy khoan / sơn dưới giá vốn cho cùng một khách, 4 lần |
| zero-vat-on-taxable-goods | `O20` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY | Hoá đơn bán thép xuất thuế 0%, sản phẩm chịu 10% |
| scrap-high-value-one-person | `O12` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Bình huỷ 6 lần hàng giá trị cao (máy khoan, máy mài, sơn) trong 2 tháng |
| expense-claimed-twice | `O23` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Hùng kê 2 lần cùng khoản tiếp khách 1.250.000 ngày 18/7 |
| expense-on-leave-day | `O24` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY | Lan kê xăng công tác ngày 6/8 khi đang nghỉ phép (trạng thái nghỉ: validate) |
| round-amount-expenses | `O25` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY, THEO_DOI | Tuấn: 8 khoản chi liền đều là số tròn |
| credit-note-after-payment | `O21` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Hoá đơn đã thu đủ, 2 tuần sau giảm trừ toàn bộ và chi trả lại tiền |
| overdue-receivables | `O15` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY, THEO_DOI | Công nợ quá hạn tự nhiên của kịch bản |
| customer-invoiced-twice | `O02` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Hoá đơn bán gạch cho Mộc An bị sao chép và ghi sổ lại cùng ngày 21/7 |
| duplicate-reversed | `O02` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Hoá đơn trùng đã được giảm trừ huỷ: đã xử lý, không báo |
| same-order-two-weeks-apart | `O02` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Cùng đơn xi măng cách 2 tuần: mua lại, không phải trùng |
| payment-split-under-limit | `O07` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Hoá đơn 33 triệu trả làm 3 lần 11 triệu trong 2 ngày, mỗi lần dưới hạn mức duyệt 20 triệu |
| three-bills-paid-one-by-one | `O07` | mồi nhử | **KHÔNG BÁO NHẦM** | — | 3 hoá đơn riêng của cùng NCC, mỗi hoá đơn trả một lần: bình thường |
| invoice-backdated-into-june | `O26` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Hoá đơn bán tạo ngày 12/7 nhưng ghi ngày 30/6: doanh thu dời vào tháng 6 |
| entry-backdated-into-august | `O26` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Bút toán tay 60 triệu tạo ngày 20/9 nhưng ghi ngày 31/8 |
| month-end-closing-entry | `O26` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Bút toán trích trước làm ngày 5/8 cho tháng 7: khoá sổ bình thường |
| vendor-bill-arrived-late | `O26` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Hoá đơn nhà cung cấp đến muộn, nhập sau: bình thường |
| quarter-end-revenue-reversed | `O27` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Hoá đơn 29/6 (cuối quý) bị giảm trừ toàn bộ ngày 6/7 |
| month-end-revenue-partly-reversed | `O27` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY | Hoá đơn 28/8 bị giảm trừ 60% ngày 3/9 |
| mid-month-sale-credited | `O27` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Hoá đơn giữa tháng 7 bị giảm trừ đầu tháng 8: không phải dồn doanh thu cuối tháng |
| month-end-sale-credited-late | `O27` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Hoá đơn 30/5 bị giảm trừ ngày 25/6, hơn 15 ngày sau |
| bill-over-5m-paid-in-cash | `O28` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY | Hoá đơn 11 triệu (gồm VAT) trả tiền mặt: 1 triệu VAT đầu vào không được khấu trừ |
| bill-over-5m-paid-by-bank | `O28` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Hoá đơn 11 triệu chuyển khoản: hợp lệ |
| same-day-bills-over-5m-in-cash | `O29` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY | 3 hoá đơn VPP cùng ngày 20/8, cộng 5,5 triệu, trả tiền mặt |
| same-day-bills-under-5m-in-cash | `O29` | mồi nhử | **KHÔNG BÁO NHẦM** | — | 2 hoá đơn cùng ngày, cộng 4,4 triệu: dưới ngưỡng, hợp lệ |
| same-day-bills-over-5m-by-bank | `O29` | mồi nhử | **KHÔNG BÁO NHẦM** | — | 3 hoá đơn cùng ngày cộng 5,5 triệu, chuyển khoản: hợp lệ |
| partial-returns-credited | `O21` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Khách trả lại một phần hàng trong 3 tuần, có giảm trừ: bình thường |
| customer-pays-in-two-parts | `O22` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Khách trả một hoá đơn làm hai lần cách nhau vài tuần: bình thường |
| bill-paid-in-instalments | `O07` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Hoá đơn NCC trả hai kỳ cách nhau một tháng: bình thường |
| discounts-above-cost | `O19` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Giảm giá 3-8% cho khách quen, vẫn trên giá vốn: bình thường |
| clearance-week-below-cost | `O19` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Tuần xả hàng cuối tháng 8: cả ba người bán đều bán sơn lót dưới giá vốn: bình thường |
| fresh-bill-twice-lowercase-ref | `O01` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Số HĐ gõ lại chữ thường, bỏ khoảng trắng, 12 ngày sau, cả hai đã trả |
| fresh-invoice-copied-two-days-later | `O02` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Hoá đơn bán ống nhựa bị sao chép và ghi sổ lại 2 ngày sau |
| fresh-bill-paid-in-four-parts | `O07` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY | Hoá đơn 36 triệu trả làm 4 lần 9 triệu trong 3 ngày, mỗi lần dưới hạn mức 20 triệu |
| fresh-scrap-grinders | `O12` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Hùng huỷ 15 máy mài trong 6 tuần |
| fresh-customer-twin-same-email | `O13` | cài sẵn | **BẮT ĐƯỢC** | THEO_DOI | Hai hồ sơ khách cùng email |
| fresh-split-cement-orders | `O16` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY | 2 đơn xi măng cùng NCC trong 5 ngày, mỗi đơn 19,4 triệu (gồm VAT), dưới ngưỡng 20 triệu |
| fresh-vendor-bank-is-accountant | `O17` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | TK ngân hàng NCC trùng TK của kế toán Ngô Thị Mai |
| fresh-new-vendor-no-tax-code | `O18` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | NCC mới do kế toán tạo, không MST, không địa chỉ, nhận 37,3 triệu, trả sau 2 ngày |
| fresh-below-cost-three-customers | `O19` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Hùng bán máy mài 820.000 (vốn 890.000) cho 3 khách khác nhau |
| fresh-eight-percent-on-tiles | `O20` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY | Hoá đơn bán gạch xuất thuế 8%, sản phẩm chịu 10% |
| fresh-half-credited-and-paid-out | `O21` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY | Hoá đơn đã thu, 3 tuần sau giảm trừ một nửa và chi trả lại tiền |
| fresh-customer-paid-twice | `O22` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Thu tiền khách 2 lần cách nhau 2 ngày, lần sau chưa đối soát |
| fresh-parking-claimed-twice | `O23` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY | Bình kê 2 lần cùng khoản gửi xe 180.000, cách 2 ngày |
| fresh-expense-on-leave | `O24` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY | Hùng kê tiếp khách ngày 10/2 khi đang nghỉ phép |
| fresh-accountant-round-claims | `O25` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY, THEO_DOI | Mai: 6 khoản chi, khoản nào cũng tròn trăm nghìn |
| fresh-invoice-backdated-into-december | `O26` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Hoá đơn tạo ngày 9/1 nhưng ghi ngày 31/12: doanh thu dời vào năm trước |
| fresh-january-revenue-reversed | `O27` | cài sẵn | **BẮT ĐƯỢC** | XEM_NGAY | Hoá đơn 29/1 bị giảm trừ toàn bộ ngày 4/2 |
| fresh-cash-bill-over-5m | `O28` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY | Hoá đơn 6,6 triệu (gồm VAT) trả tiền mặt |
| fresh-same-day-cash-pair | `O29` | cài sẵn | **BẮT ĐƯỢC** | NEN_XU_LY | 2 hoá đơn VPP cùng ngày 16/3, cộng 5,5 triệu, trả tiền mặt |
| fresh-monthly-internet | `O01` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Cước internet 1,1 triệu mỗi tháng, số tham chiếu khác nhau |
| fresh-two-orders-same-day | `O02` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Khách đặt 2 đơn sơn giống nhau trong một ngày, mỗi đơn có đơn bán riêng |
| fresh-two-large-payments | `O07` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Hoá đơn 45 triệu trả 25 + 20 triệu: mỗi lần đều đã qua duyệt |
| fresh-company-and-its-buyer | `O13` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Công ty và người mua hàng của công ty dùng chung số máy bàn |
| fresh-two-small-orders | `O16` | mồi nhử | **KHÔNG BÁO NHẦM** | — | 2 đơn dụng cụ cùng NCC trong 3 ngày, mỗi đơn 8 triệu: nhỏ, không phải chia nhỏ |
| fresh-price-correction-unpaid | `O21` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Hoá đơn chưa thu, giảm 10% vì sai giá: không có tiền chi ra |
| fresh-two-invoices-same-amount-paid | `O22` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Khách trả 2 hoá đơn cùng số tiền cùng ngày, cả hai đã đối soát |
| fresh-team-lunch-split | `O23` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Hai người cùng kê 350.000 bữa trưa của mình cùng ngày |
| fresh-expense-on-refused-leave | `O24` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Tuấn kê xăng ngày 23/3: đơn nghỉ hôm đó bị từ chối, anh đi làm |
| fresh-invoice-made-two-days-later | `O26` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Hoá đơn ghi 30/1, làm ngày 2/2: trong hạn 3 ngày |
| fresh-month-end-small-credit | `O27` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Hoá đơn 27/2 giảm 10% ngày 3/3: dưới một nửa |
| fresh-cash-bill-under-5m | `O28` | mồi nhử | **KHÔNG BÁO NHẦM** | — | Hoá đơn 4,95 triệu trả tiền mặt: dưới ngưỡng |
| fresh-same-day-cash-two-vendors | `O29` | mồi nhử | **KHÔNG BÁO NHẦM** | — | 2 hoá đơn cùng ngày, 2 NCC khác nhau, mỗi NCC dưới 5 triệu |

Bắt được **43/43** ca cài sẵn; **29/29** mồi nhử không bị báo nhầm; **1** phát hiện ngoài đáp án (xem từng cái bên dưới).

Riêng **bộ ca mới** (viết sau lần sửa cuối, chưa dùng để chỉnh): bắt được **19/19** ca cài sẵn; **13/13** mồi nhử không bị báo nhầm.

Trên **dữ liệu thật** (Birmingham City Council, Purchase Card Transactions and Payments to Suppliers over £500, October 2024 - September 2025. Contains public sector information licensed under the Open Government Licence v3.0., không ai cài gì): **85** phát hiện: `O01` 85.

Phát hiện trên dữ liệu thật (người xem xét đúng hay báo nhầm):

- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham O125966: BILL/2026/06/0022 (2026-06-02, số HĐ —) và BILL/2026/06/0023 (2026-06-02, số HĐ —), cùng số tiền, cách nhau 0 ngày, 2.335.527.600 đ và 2.335.527.600 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham O125966: BILL/2026/04/0015 (2026-04-07, số HĐ —) và BILL/2026/04/0016 (2026-04-07, số HĐ —), cùng số tiền, cách nhau 0 ngày, 2.054.319.900 đ và 2.054.319.900 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham B138793: BILL/2026/07/0021 (2026-07-28, số HĐ —) và BILL/2026/08/0018 (2026-08-06, số HĐ —), cùng số tiền, cách nhau 9 ngày, 464.400.000 đ và 464.400.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0061 (2026-09-26, số HĐ —) và BILL/2026/09/0085 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 151.653.600 đ và 151.653.600 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0073 (2026-02-13, số HĐ —) và BILL/2026/02/0105 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 140.396.400 đ và 140.396.400 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0060 (2026-09-26, số HĐ —) và BILL/2026/09/0084 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 126.592.200 đ và 126.592.200 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0072 (2026-02-13, số HĐ —) và BILL/2026/02/0104 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 117.195.300 đ và 117.195.300 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0059 (2026-09-26, số HĐ —) và BILL/2026/09/0083 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 113.097.600 đ và 113.097.600 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0071 (2026-02-13, số HĐ —) và BILL/2026/02/0103 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 104.702.400 đ và 104.702.400 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0058 (2026-09-26, số HĐ —) và BILL/2026/09/0082 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 104.101.200 đ và 104.101.200 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0056 (2026-09-26, số HĐ —) và BILL/2026/09/0080 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 98.960.400 đ và 98.960.400 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0070 (2026-02-13, số HĐ —) và BILL/2026/02/0102 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 96.373.800 đ và 96.373.800 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0069 (2026-02-13, số HĐ —) và BILL/2026/02/0101 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 95.184.000 đ và 95.184.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0068 (2026-02-13, số HĐ —) và BILL/2026/02/0100 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 91.614.600 đ và 91.614.600 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/07/0037 (2026-07-10, số HĐ —) và BILL/2026/07/0048 (2026-07-16, số HĐ —), cùng số tiền, cách nhau 6 ngày, 89.964.000 đ và 89.964.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0055 (2026-09-26, số HĐ —) và BILL/2026/09/0079 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 89.964.000 đ và 89.964.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0052 (2026-09-26, số HĐ —) và BILL/2026/09/0077 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 83.538.000 đ và 83.538.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0067 (2026-02-13, số HĐ —) và BILL/2026/02/0099 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 83.286.000 đ và 83.286.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham B141807: BILL/2026/01/0023 (2026-01-29, số HĐ —) và BILL/2026/01/0024 (2026-01-30, số HĐ —), cùng số tiền, cách nhau 1 ngày, 81.000.000 đ và 81.000.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham O142970: BILL/2026/04/0030 (2026-04-22, số HĐ —) và BILL/2026/05/0034 (2026-05-02, số HĐ —), cùng số tiền, cách nhau 10 ngày, 79.478.400 đ và 79.478.400 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham O142970: BILL/2026/07/0029 (2026-07-15, số HĐ —) và BILL/2026/07/0030 (2026-07-22, số HĐ —), cùng số tiền, cách nhau 7 ngày, 79.478.400 đ và 79.478.400 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0065 (2026-02-13, số HĐ —) và BILL/2026/02/0098 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 77.337.000 đ và 77.337.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/06/0076 (2026-06-16, số HĐ —) và BILL/2026/06/0090 (2026-06-20, số HĐ —), cùng số tiền, cách nhau 4 ngày, 77.280.000 đ và 77.280.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0050 (2026-09-26, số HĐ —) và BILL/2026/09/0075 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 74.798.700 đ và 74.798.700 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0064 (2026-02-13, số HĐ —) và BILL/2026/02/0097 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 73.767.600 đ và 73.767.600 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/01/0066 (2026-01-06, số HĐ —) và BILL/2026/01/0067 (2026-01-06, số HĐ —), cùng số tiền, cách nhau 0 ngày, 73.752.000 đ và 73.752.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0062 (2026-02-13, số HĐ —) và BILL/2026/02/0063 (2026-02-13, số HĐ —), cùng số tiền, cách nhau 0 ngày, 73.752.000 đ và 73.752.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0063 (2026-02-13, số HĐ —) và BILL/2026/02/0095 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 73.752.000 đ và 73.752.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0095 (2026-02-14, số HĐ —) và BILL/2026/02/0096 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 0 ngày, 73.752.000 đ và 73.752.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/06/0074 (2026-06-16, số HĐ —) và BILL/2026/06/0075 (2026-06-16, số HĐ —), cùng số tiền, cách nhau 0 ngày, 73.752.000 đ và 73.752.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0049 (2026-09-26, số HĐ —) và BILL/2026/09/0074 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 72.852.000 đ và 72.852.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham B131391: BILL/2026/03/0037 (2026-03-28, số HĐ —) và BILL/2026/03/0038 (2026-03-31, số HĐ —), cùng số tiền, cách nhau 3 ngày, 71.280.000 đ và 71.280.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0061 (2026-02-13, số HĐ —) và BILL/2026/02/0094 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 69.246.300 đ và 69.246.300 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham B131391: BILL/2026/02/0028 (2026-02-26, số HĐ —) và BILL/2026/03/0036 (2026-03-06, số HĐ —), cùng số tiền, cách nhau 8 ngày, 68.904.000 đ và 68.904.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0048 (2026-09-26, số HĐ —) và BILL/2026/09/0073 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 68.649.000 đ và 68.649.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham B138125: BILL/2025/10/0021 (2025-10-17, số HĐ —) và BILL/2025/10/0026 (2025-10-24, số HĐ —), cùng số tiền, cách nhau 7 ngày, 64.947.000 đ và 64.947.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0059 (2026-02-13, số HĐ —) và BILL/2026/02/0093 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 64.533.000 đ và 64.533.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0047 (2026-09-26, số HĐ —) và BILL/2026/09/0072 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 58.842.000 đ và 58.842.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham B138125: BILL/2026/02/0013 (2026-02-17, số HĐ —) và BILL/2026/02/0015 (2026-02-20, số HĐ —), cùng số tiền, cách nhau 3 ngày, 56.828.400 đ và 56.828.400 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham B138125: BILL/2026/05/0016 (2026-05-09, số HĐ —) và BILL/2026/05/0017 (2026-05-12, số HĐ —), cùng số tiền, cách nhau 3 ngày, 56.828.400 đ và 56.828.400 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0058 (2026-02-13, số HĐ —) và BILL/2026/02/0092 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 55.314.000 đ và 55.314.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/06/0071 (2026-06-16, số HĐ —) và BILL/2026/06/0072 (2026-06-16, số HĐ —), cùng số tiền, cách nhau 0 ngày, 55.314.000 đ và 55.314.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/06/0072 (2026-06-16, số HĐ —) và BILL/2026/06/0089 (2026-06-20, số HĐ —), cùng số tiền, cách nhau 4 ngày, 55.314.000 đ và 55.314.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0046 (2026-09-26, số HĐ —) và BILL/2026/09/0071 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 50.765.400 đ và 50.765.400 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0045 (2026-09-26, số HĐ —) và BILL/2026/09/0070 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 49.035.000 đ và 49.035.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham B138125: BILL/2026/01/0017 (2026-01-20, số HĐ —) và BILL/2026/01/0020 (2026-01-21, số HĐ —), cùng số tiền, cách nhau 1 ngày, 48.710.100 đ và 48.710.100 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0057 (2026-02-13, số HĐ —) và BILL/2026/02/0091 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 46.997.100 đ và 46.997.100 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0043 (2026-09-26, số HĐ —) và BILL/2026/09/0069 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 45.110.400 đ và 45.110.400 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0056 (2026-02-13, số HĐ —) và BILL/2026/02/0088 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 41.485.500 đ và 41.485.500 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0054 (2026-02-13, số HĐ —) và BILL/2026/02/0055 (2026-02-13, số HĐ —), cùng số tiền, cách nhau 0 ngày, 36.876.000 đ và 36.876.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0055 (2026-02-13, số HĐ —) và BILL/2026/02/0085 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 36.876.000 đ và 36.876.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0085 (2026-02-14, số HĐ —) và BILL/2026/02/0086 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 0 ngày, 36.876.000 đ và 36.876.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham B138125: BILL/2025/12/0019 (2025-12-12, số HĐ —) và BILL/2025/12/0025 (2025-12-20, số HĐ —), cùng số tiền, cách nhau 8 ngày, 32.473.500 đ và 32.473.500 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0039 (2026-09-26, số HĐ —) và BILL/2026/09/0066 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 29.421.000 đ và 29.421.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/02/0053 (2026-02-13, số HĐ —) và BILL/2026/02/0081 (2026-02-14, số HĐ —), cùng số tiền, cách nhau 1 ngày, 27.657.000 đ và 27.657.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0038 (2026-09-26, số HĐ —) và BILL/2026/09/0065 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 26.989.200 đ và 26.989.200 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham O144815: BILL/2025/10/0048 (2025-10-18, số HĐ —) và BILL/2025/11/0046 (2025-11-01, số HĐ —), cùng số tiền, cách nhau 14 ngày, 26.100.000 đ và 26.100.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham C103586: BILL/2026/09/0037 (2026-09-26, số HĐ —) và BILL/2026/09/0064 (2026-09-30, số HĐ —), cùng số tiền, cách nhau 4 ngày, 25.218.000 đ và 25.218.000 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham B106115: BILL/2026/02/0044 (2026-02-20, số HĐ —) và BILL/2026/03/0048 (2026-03-06, số HĐ —), cùng số tiền, cách nhau 14 ngày, 25.216.200 đ và 25.216.200 đ. Cả hai đã được thanh toán.
- `O01` [NEN_XU_LY] Hoá đơn mua có thể bị nhập hai lần và đã trả hai lần: NCC Birmingham B106115: BILL/2025/10/0089 (2025-10-24, số HĐ —) và BILL/2025/10/0100 (2025-10-31, số HĐ —), cùng số tiền, cách nhau 7 ngày, 25.038.000 đ và 25.038.000 đ. Cả hai đã được thanh toán.

Phát hiện ngoài đáp án:

- `O24` [NEN_XU_LY] Chi phí công việc vào ngày đang nghỉ phép: Lê Văn Hùng kê 'Văn phòng phẩm' 200.500 đ ngày 2026-02-11, trong kỳ nghỉ Unpaid leave 2026-02-09 – 2026-02-11 đã được duyệt.
