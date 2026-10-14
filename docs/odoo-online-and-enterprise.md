# Odoo Online and Odoo Enterprise

*Tiếng Việt ở cuối trang.*

odoo-cli is verified on **Odoo Community**: the stock image of each series, rebuilt and re-checked in
the open every week ([conformance.md](conformance.md)). This page says what is known about Odoo
Online and Odoo Enterprise, how that was found out, and what it does not promise.

## Where odoo-cli runs

| Odoo | What is known | Can you re-check it? |
|---|---|---|
| Community 19.0, self-hosted | 1272 of 1358 operations verified | Yes: the conformance workflow, every week |
| Community 20.0, self-hosted | 1170 of the 1296 operations 20.0 can run verified; runs with `--allow-unverified` | Yes, the same workflow |
| Enterprise 20.0 on Odoo Online | checked once on 2026-10-14 (below): reads, writes and buttons; not counted as verified | Only on a database of your own, with the read check below |
| Enterprise, self-hosted or on Odoo.sh | not tested: Enterprise is not public, so no lab can be built from it | — |

## The test of 2026-10-14

- **What was tested:** a trial database on Odoo Online, running `20.0+e`, with 80 apps installed,
  Studio unused, and Odoo's sample data where an app offered it.
- **How odoo-cli connected:** through Odoo's JSON-2 API, with an API key of scope **RPC**.
- **Reads, one record each:** of the 291 list operations, odoo-cli ran **269**, with no error.
  - 9 were refused because 20.0 removed their model.
  - 13 were refused because a non-application module they need was not installed (landed costs,
    purchase agreements, Viettel e-invoices).
- **Reads, checked one by one** against what Odoo itself returns, with the same comparison as the
  weekly conformance run, on the 580 reads of the catalog:
  - **0 disagreed** with Odoo;
  - 194 agreed on records (3 of them with fields the independent reader cannot check), and 172 on
    models with no records;
  - 170 found no record to read in the trial's data, 26 need a module that was not installed, and 18
    name a model 20.0 removed (odoo-cli refuses those before contacting Odoo).

  The evidence (hashes and statuses only) is
  `docs/odoo-surface/evidence/read-conformance-20.0e-odoo-online-trial-20261014.json`.
- **Writes, buttons and dialogs**, with the same comparison, on the trial's own sample data:
  - creates and updates: **415 of 553 agreed** with Odoo. One disagreed, `catalog.pricelist_item.create`
    (its `applied_on`), as it does on Community 20.0, where it is not counted as verified either. The
    rest found no record, no value a generic test satisfies, or a model 20.0 removed;
  - business buttons and dialogs: **75 of 192 agreed**. One disagreed on Enterprise only:
    `finance.reconcile_model.set_manual` also changes `can_be_proposed`, which the Enterprise web client
    leaves alone. Most of the others found no pair of identical records in the trial's small data set.

  This is why odoo-cli holds writes back on Enterprise until you accept them: Enterprise extends
  Community's models, and a button can behave differently there. Evidence:
  `docs/odoo-surface/evidence/{write,action}-conformance-20.0e-odoo-online-trial-20261014.json`.

## What that means for you

- **The API needs the Custom plan.** Odoo's pricing lists "External API" on its Custom plan only. A
  trial database answered the API; a Standard subscription may not. Without the API, odoo-cli (or any
  external tool) cannot reach the database.
- **Every answer is marked unverified.** odoo-cli checks the Odoo it reaches before it acts
  ([provider-fit.md](provider-fit.md)). Enterprise modules extend Community models, so on Enterprise
  odoo-cli:
  - runs reads, with a note;
  - holds back writes (`AERP_ENVIRONMENT_UNVERIFIED`) until you pass `--allow-unverified`, at your own
    risk.
- **Odoo Online changes version on its own.** It moves to new releases every few months. odoo-cli
  refuses an intermediate release (`saas~`) it has no catalog for, and says so.
- **Enterprise-only apps are not in the catalog.** Rental, Planning, Appointments, Field Service,
  Helpdesk and the like are not covered.

To check reads on your own Odoo Online or Enterprise database (read-only; it changes nothing):

```sh
export ODOO_ORACLE_PASSWORD="$AERP_CREDENTIAL_MY_ODOO"   # the same API key, for the independent reader
python3 conformance/odoo-read-conformance.py --odoo-bin odoo-cli --profile my-odoo \
  --endpoint https://mycompany.odoo.com --database mycompany --login me@example.com \
  --series 20.0 --image-digest odoo-online --renames docs/odoo-surface/dialect-renames-20.0.json \
  --readiness ANY --evidence /tmp/read-check.json
```

## Odoo's own MCP server

Odoo 20 Enterprise ships an MCP server: an AI agent connects to `<database>/mcp` with an API key of
scope **MCP** and acts with that user's rights. On the trial database of 2026-10-14 it offered five
tools, all read-only:

| Tool | What it does |
|---|---|
| `ai_tool_get_models` | the models the user can reach |
| `ai_tool_get_fields` | a model's searchable fields |
| `ai_tool_mcp_retrieve_initial_context` | the user, their time zone and companies |
| `ai_tool_search` | records matching a domain |
| `ai_tool_read_group` | groups with sums, averages and counts |

Anything else (a write, a business button, a print) is a server action an administrator writes and
exposes.

The two answer different needs:

| | Odoo's MCP server | odoo-cli |
|---|---|---|
| Odoo editions | Enterprise | Community (verified), Enterprise (checked once, above) |
| What it offers by default | five read tools: search, read and group on any model | 587 verified reads, plus the same generic reads on any model (`explore`); with `--allow-writes`, writes, business buttons, dialogs and guarded deletes too (high-risk ones after the person confirms) |
| A business button (confirm, post, validate) | only through a server action an administrator writes | an operation compared with the web client's own result |
| Before acting | Odoo's access rights | Odoo's access rights, plus the pre-flight check and `--confirm-impact` on deletes |
| How an agent uses it | MCP | MCP (`odoo-cli mcp serve`), or as a command from any agent that runs commands |
| Outside an agent | — | a shell, a script, a notebook, CI, and the [Python SDK](https://github.com/asao-vn/asao-erp-sdk) |

Both can serve one agent: Odoo's server for quick questions on an Enterprise database, odoo-cli for
verified operations and for anything scripted.

---

## Tiếng Việt: Odoo Online và Odoo Enterprise

odoo-cli được kiểm chứng trên **Odoo Community** (image chuẩn, kiểm lại công khai mỗi tuần). Trang này ghi
những gì đã biết về Odoo Online và Enterprise.

**Lần thử ngày 14/10/2026:**
- **Đối tượng:** một database dùng thử trên Odoo Online, bản `20.0+e`, 80 app, không dùng Studio.
- **Kết nối:** qua API JSON-2, bằng API key phạm vi **RPC**.
- **Đọc thử mỗi thao tác 1 bản ghi:** odoo-cli chạy được **269/291** thao tác đọc danh sách, không lỗi
  nào.
  - 9 thao tác bị từ chối vì bản 20 đã bỏ model.
  - 13 thao tác bị từ chối vì thiếu module phụ.
- **Đối chiếu từng kết quả đọc với Odoo** (580 thao tác đọc):
  - **0 thao tác lệch**;
  - 194 khớp trên dữ liệu thật, 172 khớp trên model rỗng;
  - phần còn lại không chạy được vì bản dùng thử không có bản ghi, thiếu module, hoặc model đã bị bản 20
    bỏ.
- **Thao tác ghi, nút nghiệp vụ và hộp thoại** (trên dữ liệu mẫu của bản dùng thử):
  - tạo và sửa: **415/553 khớp**. 1 lệch (`catalog.pricelist_item.create`), lệch y như trên Community 20;
  - nút nghiệp vụ và hộp thoại: **75/192 khớp**. 1 lệch riêng trên Enterprise: `finance.reconcile_model.set_manual`
    đổi thêm một trường mà giao diện web của Enterprise không đổi;
  - phần lớn số còn lại không chạy được vì dữ liệu mẫu ít.

  Đây là lý do odoo-cli giữ lại thao tác ghi trên Enterprise cho đến khi bạn chấp nhận.

**Cần biết:**
- **API cần gói Custom.** Theo bảng giá của Odoo, "External API" chỉ có ở gói Custom. Bản dùng thử thì
  gọi được API, nhưng gói Standard có thể không.
- **Mọi kết quả trên Enterprise bị gắn nhãn chưa kiểm chứng.**
  - Thao tác đọc vẫn chạy.
  - Thao tác ghi bị giữ lại, trừ khi bạn tự chịu rủi ro bằng `--allow-unverified`.
- **Odoo Online tự nâng phiên bản** vài tháng một lần.
- **App chỉ có trên Enterprise** (Rental, Planning, Appointments, Field Service, Helpdesk…) chưa có
  trong catalog.
- **Enterprise tự host hoặc trên Odoo.sh:** chưa thử, vì không có image công khai để dựng lab.

**MCP riêng của Odoo 20 Enterprise.** Agent nối vào `<database>/mcp` bằng key phạm vi MCP.
- Trên bản dùng thử ngày 14/10/2026, nó đưa ra **5 công cụ, đều chỉ đọc**: danh sách model, danh sách
  trường, ngữ cảnh người dùng, tìm kiếm, gom nhóm.
- Muốn ghi, bấm nút nghiệp vụ hay in, quản trị viên phải tự viết và mở từng server action.
- odoo-cli thì có sẵn 1358 thao tác đặt tên rõ (đọc, ghi, nút nghiệp vụ, hộp thoại, in, xoá có kiểm soát),
  đã đối chiếu với chính Odoo, chạy được trên Community, trong script và notebook.
- Hai thứ dùng chung được trong một agent.
