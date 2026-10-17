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
| round-amount-expenses | `O25` | cài sẵn | **BẮT ĐƯỢC** | THEO_DOI | Tuấn: 8 khoản chi liền đều là số tròn |
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

Bắt được **24/24** ca cài sẵn; **11/11** mồi nhử không bị báo nhầm; **0** phát hiện ngoài đáp án (xem từng cái bên dưới).

Trên **dữ liệu bán hàng thật** (Chen, D. (2012). Online Retail II [Dataset]. UCI Machine Learning Repository. https://doi.org/10.24432/C5CG6D, không ai cài gì): **38** phát hiện: `O19` 2, `O21` 36.

Phát hiện trên dữ liệu thật (người xem xét đúng hay báo nhầm):

- `O19` [XEM_NGAY] Phạm Thị Lan bán dưới giá vốn 18 lần: Phạm Thị Lan: 18 dòng bán dưới giá vốn, lỗ so với giá vốn 15.110.000 đ; 6 trong số đó cho cùng khách 'Khách online UCI 14432'. INV/2025/00183 [UCI-70006] LOVE HEART POCKET WARMER [70006]: bán 13.000 đ, vốn 35.000 đ; INV/2025/00183 [UCI-85184C] S/4 VALENTINE DECOUPAGE HEART BOX [85184C]: bán 38.000 đ, vốn 62.000 đ; INV/2025/00184 [UCI-22655] VINTAGE RED KITCHEN CABINET [22655]: bán 3.750.000 đ, vốn 4.410.000 đ; INV/2026/00673 [UCI-22116] METAL SIGN HIS DINNER IS SERVED [22116]: bán 24.000 đ, vốn 39.000 đ; INV/2026/00694 [UCI-22402] MAGNETS PACK OF 4 VINTAGE COLLAGE [22402]: bán 12.000 đ, vốn 18.000 đ; INV/2026/00701 [UCI-21823] PAINTED METAL HEART WITH HOLLY BELL [21823]: bán 12.000 đ, vốn 31.000 đ
- `O21` [XEM_NGAY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00623 (2026-05-24, Khách online UCI 16525, 15.043.600 đ, người bán Lê Văn Hùng) bị giảm trừ bằng RINV/2026/00028 ngày 2026-06-03 (10 ngày sau), 15.043.600 đ, và số tiền này đã được chi trả lại.
- `O21` [XEM_NGAY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00574 (2026-04-09, Khách online UCI 17731, 10.203.600 đ, người bán Đỗ Minh Tuấn) bị giảm trừ bằng RINV/2026/00022 ngày 2026-04-26 (17 ngày sau), 11.921.800 đ, và số tiền này đã được chi trả lại.
- `O19` [XEM_NGAY] Lê Văn Hùng bán dưới giá vốn 8 lần: Lê Văn Hùng: 8 dòng bán dưới giá vốn, lỗ so với giá vốn 3.026.000 đ; 2 trong số đó cho cùng khách 'Khách online UCI 13985'. INV/2025/00172 [UCI-10135] COLOURING PENCILS BROWN TUBE [10135]: bán 13.000 đ, vốn 27.000 đ; INV/2025/00177 [UCI-22377] BOTTLE BAG RETROSPOT [22377]: bán 26.000 đ, vốn 31.000 đ; INV/2025/00177 [UCI-70006] LOVE HEART POCKET WARMER [70006]: bán 13.000 đ, vốn 35.000 đ; INV/2025/00191 [UCI-21876] POTTERING MUG [21876]: bán 13.000 đ, vốn 27.000 đ; INV/2026/00509 [UCI-22655] VINTAGE RED KITCHEN CABINET [22655]: bán 3.750.000 đ, vốn 4.410.000 đ; INV/2026/00742 [UCI-21208] PASTEL COLOUR HONEYCOMB FAN [21208]: bán 0 đ, vốn 8.000 đ
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2025/00163 (2025-10-08, Khách online UCI 15973, 10.172.800 đ, người bán Phạm Thị Lan) bị giảm trừ bằng RINV/2025/00007 ngày 2025-11-04 (27 ngày sau), 6.494.400 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00677 (2026-07-19, Khách online UCI 16689, 9.735.000 đ, người bán Đỗ Minh Tuấn) bị giảm trừ bằng RINV/2026/00039 ngày 2026-07-26 (7 ngày sau), 3.960.000 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2025/00165 (2025-10-08, Khách online UCI 17655, 22.382.800 đ, người bán Đỗ Minh Tuấn) bị giảm trừ bằng RINV/2025/00004 ngày 2025-10-16 (8 ngày sau), 2.288.000 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00536 (2026-03-05, Khách online UCI 13883, 3.724.600 đ, người bán Lê Văn Hùng) bị giảm trừ bằng RINV/2026/00012 ngày 2026-03-12 (7 ngày sau), 2.157.100 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2025/00186 (2025-11-13, Khách online UCI 15694, 13.120.800 đ, người bán Phạm Thị Lan) bị giảm trừ bằng RINV/2025/00016 ngày 2025-12-02 (19 ngày sau), 1.848.000 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00516 (2026-02-06, Khách online UCI 16325, 6.270.000 đ, người bán Lê Văn Hùng) bị giảm trừ bằng RINV/2026/00009 ngày 2026-02-17 (11 ngày sau), 1.742.400 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2025/00176 (2025-10-16, Khách online UCI 13093, 60.365.800 đ, người bán Đỗ Minh Tuấn) bị giảm trừ bằng RINV/2025/00009 ngày 2025-11-12 (27 ngày sau), 1.502.600 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2025/00231 (2025-12-31, Khách online UCI 16217, 6.034.600 đ, người bán Phạm Thị Lan) bị giảm trừ bằng RINV/2026/00003 ngày 2026-01-15 (15 ngày sau), 1.243.000 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00492 (2026-01-21, Khách online UCI 14794, 9.358.800 đ, người bán Đỗ Minh Tuấn) bị giảm trừ bằng RINV/2026/00005 ngày 2026-01-28 (7 ngày sau), 1.117.600 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00628 (2026-05-26, Khách online UCI 13090, 31.979.200 đ, người bán Đỗ Minh Tuấn) bị giảm trừ bằng RINV/2026/00031 ngày 2026-06-19 (24 ngày sau), 1.115.400 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2025/00178 (2025-10-16, Khách online UCI 14309, 4.925.800 đ, người bán Lê Văn Hùng) bị giảm trừ bằng RINV/2025/00015 ngày 2025-11-20 (35 ngày sau), 1.085.700 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00551 (2026-03-17, Khách online UCI 16218, 30.830.800 đ, người bán Lê Văn Hùng) bị giảm trừ bằng RINV/2026/00017 ngày 2026-04-07 (21 ngày sau), 987.800 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00491 (2026-01-21, Khách online UCI 16713, 6.494.400 đ, người bán Phạm Thị Lan) bị giảm trừ bằng RINV/2026/00006 ngày 2026-01-28 (7 ngày sau), 943.800 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2025/00194 (2025-11-24, Khách online UCI 12559, 16.055.600 đ, người bán Đỗ Minh Tuấn) bị giảm trừ bằng RINV/2025/00017 ngày 2025-12-03 (9 ngày sau), 840.400 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00637 (2026-06-10, Khách online UCI 12384, 9.652.500 đ, người bán Lê Văn Hùng) bị giảm trừ bằng RINV/2026/00036 ngày 2026-07-22 (42 ngày sau), 629.200 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00680 (2026-07-22, Khách online UCI 16626, 10.366.400 đ, người bán Phạm Thị Lan) bị giảm trừ bằng RINV/2026/00042 ngày 2026-07-29 (7 ngày sau), 605.000 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00670 (2026-07-13, Khách online UCI 15755, 10.934.000 đ, người bán Lê Văn Hùng) bị giảm trừ bằng RINV/2026/00035 ngày 2026-07-20 (7 ngày sau), 602.800 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00646 (2026-06-16, Khách online UCI 16713, 12.269.400 đ, người bán Phạm Thị Lan) bị giảm trừ bằng RINV/2026/00041 ngày 2026-07-28 (42 ngày sau), 561.000 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2025/00197 (2025-12-01, Khách online UCI 16403, 5.874.000 đ, người bán Phạm Thị Lan) bị giảm trừ bằng RINV/2025/00018 ngày 2025-12-11 (10 ngày sau), 547.800 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00558 (2026-03-22, Khách online UCI 12909, 20.631.600 đ, người bán Lê Văn Hùng) bị giảm trừ bằng RINV/2026/00016 ngày 2026-04-01 (10 ngày sau), 488.400 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00764 (2026-09-06, Khách online UCI 14868, 18.466.800 đ, người bán Đỗ Minh Tuấn) bị giảm trừ bằng RINV/2026/00051 ngày 2026-09-14 (8 ngày sau), 460.900 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00629 (2026-05-28, Khách online UCI 13813, 12.744.600 đ, người bán Đỗ Minh Tuấn) bị giảm trừ bằng RINV/2026/00029 ngày 2026-06-05 (8 ngày sau), 443.300 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2025/00164 (2025-10-08, Khách online UCI 16713, 13.703.800 đ, người bán Phạm Thị Lan) bị giảm trừ bằng RINV/2025/00005 ngày 2025-10-20 (12 ngày sau), 424.600 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00476 (2026-01-07, Khách online UCI 13093, 10.082.600 đ, người bán Đỗ Minh Tuấn) bị giảm trừ bằng RINV/2026/00002 ngày 2026-01-15 (8 ngày sau), 420.200 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2025/00207 (2025-12-09, Khách online UCI 16713, 15.243.800 đ, người bán Phạm Thị Lan) bị giảm trừ bằng RINV/2025/00022 ngày 2025-12-19 (10 ngày sau), 391.600 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2025/00163 (2025-10-08, Khách online UCI 15973, 10.172.800 đ, người bán Phạm Thị Lan) bị giảm trừ bằng RINV/2026/00008 ngày 2026-02-16 (131 ngày sau), 360.800 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00675 (2026-07-16, Khách online UCI 12995, 6.969.600 đ, người bán Đỗ Minh Tuấn) bị giảm trừ bằng RINV/2026/00038 ngày 2026-07-24 (8 ngày sau), 352.000 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00568 (2026-04-05, Khách online UCI 14659, 7.172.000 đ, người bán Lê Văn Hùng) bị giảm trừ bằng RINV/2026/00024 ngày 2026-05-17 (42 ngày sau), 327.800 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00568 (2026-04-05, Khách online UCI 14659, 7.172.000 đ, người bán Lê Văn Hùng) bị giảm trừ bằng RINV/2026/00025 ngày 2026-05-17 (42 ngày sau), 327.800 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00597 (2026-04-27, Khách online UCI 18193, 8.047.600 đ, người bán Đỗ Minh Tuấn) bị giảm trừ bằng RINV/2026/00023 ngày 2026-05-12 (15 ngày sau), 327.800 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00672 (2026-07-14, Khách online UCI 12909, 10.947.200 đ, người bán Lê Văn Hùng) bị giảm trừ bằng RINV/2026/00037 ngày 2026-07-24 (10 ngày sau), 243.100 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2025/00210 (2025-12-12, Khách online UCI 17512, 16.284.400 đ, người bán Lê Văn Hùng) bị giảm trừ bằng RINV/2025/00023 ngày 2025-12-19 (7 ngày sau), 162.800 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00525 (2026-02-16, Khách online UCI 16713, 5.029.200 đ, người bán Phạm Thị Lan) bị giảm trừ bằng RINV/2026/00034 ngày 2026-07-13 (147 ngày sau), 149.600 đ, và số tiền này đã được chi trả lại.
- `O21` [NEN_XU_LY] Hoá đơn đã thu tiền bị giảm trừ và chi trả lại: INV/2026/00536 (2026-03-05, Khách online UCI 13883, 3.724.600 đ, người bán Lê Văn Hùng) bị giảm trừ bằng RINV/2026/00014 ngày 2026-03-23 (18 ngày sau), 129.800 đ, và số tiền này đã được chi trả lại.

