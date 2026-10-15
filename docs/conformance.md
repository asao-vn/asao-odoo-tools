# How odoo-cli is checked against Odoo, and how to read the result

*Tiếng Việt ở cuối trang.*

`odoo-cli` offers 1368 operations on Odoo Community: reads, creates and updates, business buttons,
dialogs, prints and deletes. An operation is called **verified** only after it was run on a real Odoo
and its result was compared with what Odoo itself did. This page explains how that comparison works,
how to read the report it produces, what it does **not** prove, and how to run it yourself.

## In one paragraph

A script builds a fresh Odoo from the official public image, with Odoo's own demo data. It then runs
every operation of the catalog through `odoo-cli`. A separate program, which does not use `odoo-cli`
and talks to Odoo directly over its JSON-RPC API, checks each result. When the two agree, the
operation is verified on that Odoo version. The script, the images and the evidence are all in this
repository, and the [conformance workflow](../.github/workflows/conformance.yml) runs it in the
open, every week and on every release.

## The Odoo it runs on

| | Odoo 19.0 | Odoo 20.0 |
|---|---|---|
| Image | the official `odoo` image on Docker Hub, pinned by digest | same |
| Data | Odoo's own demo data | same |
| Modules | every module some operation needs (55 modules) | the same set, minus four 20.0 ships inside others (51) |

The digests and the module lists are in
[`conformance/odoo-conformance-labs.json`](../conformance/odoo-conformance-labs.json). Pinning the
digest means the run always uses the exact same Odoo build, so a change in the result comes from
odoo-cli, not from a different Odoo.

Before running, the script also:
- gives the demo administrator accounting rights;
- lets the Demo payment provider capture payments by hand;
- adds one record to the few models demo data leaves empty.

## What "agree" means for each kind of operation

| Kind | What odoo-cli does | What the independent checker does | They agree when |
|---|---|---|---|
| **List** | lists records | runs the same search in Odoo | the same records, in the same order |
| **Describe** | reads one record | reads the same record | every field has the same value |
| **Create / update** | creates or changes a record | reads the record back from Odoo | Odoo holds exactly the values that were sent |
| **Business button** (Confirm, Post, Validate…) | presses it on one record | presses the same button the way the web client does, on an identical "twin" record | both records change in the same way, or Odoo refuses both alike |
| **Dialog** (wizards) | answers the dialog | opens and answers it like the web client | same as a button |
| **Print** | downloads the PDF | downloads it from the web client | the same pages and the same words |
| **Delete** | deletes, after checking what depends on the record | — | the record is gone and nothing that depended on it was left broken |

**Twins.** A button changes the record it is pressed on, so it cannot be pressed twice on one record.
The checker therefore picks two records that are identical in what matters (state, partner, company,
type…). odoo-cli presses the button on one, and the checker presses it on the other.

## How to read the report

Each run ends with a table like this (from a run of 2026-10-13 on 20.0):

| | Operations |
|---|---:|
| Re-agreed with Odoo | 1148 (98.1%) |
| **Regressed** (the CLI and Odoo disagree) | **0** |
| Unstable (agreed in one pass, disagreed in another) | 2 |
| Stale (the operation changed since its record) | 0 |
| Not exercised on this lab's data | 20 |
| Agreed, not in the manifest yet | 3 |

The rows mean:
- **Re-agreed.** The operation was already verified, and it agreed again in this run.
- **Regressed.** An operation that was verified now gives a result Odoo does not. Reads run twice
  and a disagreeing button is pressed again on another pair of twins before this is decided. **This is the
  only row that fails the run**, and it means something must be fixed before release.
- **Unstable.** It agreed once and disagreed once in the same run. The result depends on timing or
  on the order things ran in, so a person looks at it.
- **Stale.** The operation's definition changed after it was verified, so it needs verifying again.
- **Not exercised.** The fresh lab had no record in the right state to try it on: no draft to
  confirm, no twin, or a business rule demo data does not meet. It stays verified on the evidence
  where it was verified. This is not a failure.
- **Agreed, not in the manifest yet.** It agreed, but it is not counted as verified until a person
  reviews it and adds it to the manifest.

Under "Not exercised", the report counts the reasons:

| Reason | Meaning |
|---|---|
| `NOT_RUN_NO_RECORD` | the model has no record to read or act on in this lab |
| `NOT_RUN_NO_TWINS` | no two identical records in the state the button needs |
| `NOT_RUN_UNSATISFIABLE` | a create needs a value no generic test value satisfies |
| `NOT_RUN_NO_SIMPLE_FIELD` | an update has no plain field to change and restore |
| `NOT_RUN_NO_BUTTON` | the button is not on this Odoo's form |
| `INCONCLUSIVE_TWINS_DIFFER` | the twins turned out to differ, so the result proves nothing either way |
| `CLI_ERROR`, `ORACLE_ERROR` | odoo-cli or the checker got an error from Odoo, usually a business rule ("an expense needs a category") |
| `NOT_IN_RUN` | verified by an earlier, dedicated run that this weekly run does not repeat |

The full evidence of each run, one record per operation with hashes and statuses (never your data,
never a password), is attached to the run and to the release as `conformance-<version>.tar.gz`.

## What it does not prove

- **Your Odoo.** It proves odoo-cli on the stock Community image with demo data. An Odoo with extra
  modules, custom fields, automations or Studio changes may behave differently. odoo-cli checks the
  Odoo it reaches before acting ([provider-fit.md](provider-fit.md)) and tells you when it differs.
- **Odoo Enterprise, Odoo Online and Odoo.sh.** They are not tested. Odoo Online offers the external
  API on its Custom plan only.
- **Everything about your data.** A create can still be refused by a rule your company configured.
  The check proves odoo-cli does what Odoo's own web client would do, not that every request is
  accepted.
- **Speed and load.** It runs operations one at a time on a small lab.

## Run it yourself

You need Docker or Podman, Go, Python 3, about 4 GB of free memory and 3 GB of disk.

```sh
git clone https://github.com/asao-vn/asao-odoo-tools && cd asao-odoo-tools
conformance/odoo-conformance-lab.sh --series 20.0 --out /tmp/conformance-20
cat /tmp/conformance-20/summary.md
```

It takes about an hour for one series. The lab is removed at the end; pass `--keep` to look at it,
at `http://127.0.0.1:58740` for 20.0 (58739 for 19.0), login `admin`, password `admin`. The lab is
local and lives only for the run, so the demo password is safe there. Never point the script at an
Odoo you use: it creates and changes records.

---

## Tiếng Việt: odoo-cli được kiểm chứng thế nào, và đọc kết quả ra sao

`odoo-cli` có 1368 thao tác trên Odoo Community. Một thao tác chỉ được gọi là **đã kiểm chứng** khi nó đã
chạy trên một Odoo thật và kết quả được đối chiếu với chính Odoo.

**Tóm tắt.** Một script dựng Odoo mới tinh từ image chính hãng, có dữ liệu demo của Odoo. Nó chạy mọi thao
tác qua `odoo-cli`. Một chương trình độc lập, không dùng `odoo-cli` mà gọi thẳng API JSON-RPC của Odoo,
kiểm lại từng kết quả. Hai bên khớp thì thao tác được kiểm chứng trên phiên bản Odoo đó. Workflow
`conformance` chạy việc này công khai mỗi tuần và mỗi lần phát hành.

**Odoo dùng để kiểm.** Image `odoo` chính hãng trên Docker Hub, ghim theo digest (nên lần nào cũng đúng một
bản build). Dữ liệu là dữ liệu demo của Odoo. Module là mọi module mà một thao tác nào đó cần: 55 module
trên 19.0, 51 trên 20.0. Danh sách nằm ở `conformance/odoo-conformance-labs.json`.

**"Khớp" nghĩa là gì:**
- **Danh sách, xem chi tiết:** cùng bản ghi, cùng giá trị từng trường.
- **Tạo, sửa:** đọc lại từ Odoo thấy đúng giá trị đã gửi.
- **Nút nghiệp vụ** (Xác nhận, Ghi sổ, Kiểm tra…) **và hộp thoại:**
  - odoo-cli bấm trên một bản ghi;
  - bộ kiểm bấm như trình duyệt trên một bản ghi "sinh đôi" giống hệt;
  - hai bản ghi phải đổi giống nhau, hoặc Odoo từ chối cả hai như nhau.
- **In:** PDF trùng từng trang, từng chữ với bản tải từ trình duyệt.
- **Xoá:** bản ghi mất đi và không để lại dữ liệu phụ thuộc bị hỏng.

**Đọc bảng kết quả:**

| Dòng | Nghĩa |
|---|---|
| Khớp lại (Re-agreed) | đã kiểm chứng trước đây, lần này vẫn khớp |
| **Hồi quy (Regressed)** | đã kiểm chứng nhưng nay lệch với Odoo; phần đọc chạy hai lần và nút bị lệch được bấm lại trên một cặp sinh đôi khác trước khi kết luận. **Chỉ dòng này làm lần chạy báo lỗi.** |
| Không ổn định (Unstable) | trong cùng lần chạy, một lần khớp một lần lệch (do thời điểm hoặc thứ tự); cần người xem |
| Lỗi thời (Stale) | định nghĩa thao tác đã đổi sau khi kiểm chứng; cần kiểm lại |
| Không chạy được (Not exercised) | lab mới không có bản ghi ở đúng trạng thái để thử; không phải lỗi |
| Khớp nhưng chưa vào manifest | khớp, nhưng chỉ tính là đã kiểm chứng sau khi có người duyệt |

Bằng chứng mỗi lần chạy (chỉ có mã băm và trạng thái, không có dữ liệu hay mật khẩu) được đính kèm vào lần
chạy và vào bản phát hành.

**Điều nó KHÔNG chứng minh:**
- **Odoo của bạn:** module thêm, trường tự tạo, tự động hoá hay Studio có thể làm khác đi. odoo-cli kiểm
  tra Odoo trước khi chạy và báo khi Odoo khác bản chuẩn.
- **Odoo Enterprise, Odoo Online, Odoo.sh:** chưa được kiểm.
- **Quy tắc riêng của doanh nghiệp bạn:** một yêu cầu vẫn có thể bị quy tắc đó từ chối.
- **Tốc độ và tải.**

**Tự chạy:** cần Docker hoặc Podman, Go, Python 3, khoảng 4 GB RAM trống và 3 GB ổ đĩa. Chạy
`conformance/odoo-conformance-lab.sh --series 20.0 --out /tmp/conformance-20`, mỗi phiên bản mất khoảng một
giờ. Đừng bao giờ trỏ script vào Odoo đang dùng thật, vì nó tạo và sửa bản ghi.
