# A team on one Odoo: `odoo-cli spec --backend`

`odoo-cli spec plan` and `spec apply` bring an Odoo to a YAML description of a company. When several
people, scripts or AI agents work on the same Odoo, they need a shared place. A **backend** is that
place:

- **the lock:** one apply at a time;
- **the plans:** each plan is saved under its hash, with the spec as it was written, so somebody else
  can review it;
- **the record:** every apply is kept, whether it was refused, failed or applied, with its plan and
  report;
- **the spec last applied**, which a teammate can pull back.

It looks like Terraform with an S3 backend. There is one difference: **there is no state file**. A
spec names its records by their own fields, and every plan reads them from Odoo itself, so there is
nothing to keep in step with Odoo and nothing to lose.

## Where it lives

```sh
# An S3 bucket (AWS). Keys from AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY, or ~/.aws/credentials.
odoo-cli spec plan --profile company --file company.yaml --backend s3://my-bucket/odoo/company

# Cloudflare R2, MinIO, Backblaze B2: any S3 API, with its endpoint.
export AWS_ENDPOINT_URL=https://<account>.r2.cloudflarestorage.com AWS_REGION=auto
odoo-cli spec plan --profile company --file company.yaml --backend s3://my-bucket/odoo/company

# A folder this machine can write, or one the team shares.
odoo-cli spec plan --profile company --file company.yaml --backend /srv/odoo-specs/company
```

You can also name the backend once in the spec, `backend: s3://my-bucket/odoo/company`, or in
`AERP_SPEC_BACKEND`. `--backend none` sets it aside for one run.

Requests are signed with AWS Signature Version 4 by odoo-cli itself, so no AWS SDK is needed. The
lock is a create-if-absent (`If-None-Match: *`). odoo-cli checks that the store honours it: a store
that overwrites instead cannot hold a lock, and odoo-cli says so rather than pretending.

### The keys a backend needs

Grant the keys a team uses for odoo-cli this much and no more:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {"Effect": "Allow", "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject"],
     "Resource": "arn:aws:s3:::my-bucket/odoo/company/*"},
    {"Effect": "Allow", "Action": "s3:ListBucket", "Resource": "arn:aws:s3:::my-bucket",
     "Condition": {"StringLike": {"s3:prefix": "odoo/company/*"}}}
  ]
}
```

A backend belongs to **one** Odoo. The first plan records which one (`target.json`). After that, a
profile that points at another Odoo, or another database, is refused before that Odoo is read. Give
each Odoo its own prefix.

## The commands

```sh
odoo-cli spec plan  --profile company --file company.yaml               # plans, and saves the plan under its hash
odoo-cli spec pull  --backend s3://my-bucket/odoo/company --plan sha256:... --to review.yaml   # a reviewer reads exactly that spec
odoo-cli spec apply --profile company --file company.yaml --plan-hash sha256:...              # takes the lock, applies, records, releases
odoo-cli spec state --backend s3://my-bucket/odoo/company               # the Odoo, the lock, the spec last applied, the latest runs
odoo-cli spec runs  --backend s3://my-bucket/odoo/company [--run <id>]  # every apply, or one with its plan and report
odoo-cli spec pull  --backend s3://my-bucket/odoo/company --to company.yaml   # the spec last applied in full
odoo-cli spec unlock --backend s3://my-bucket/odoo/company --lock-id <id>     # only for an apply that died
```

- **Apply needs a reviewed plan.** With a backend, `spec apply` needs `--plan-hash`, the hash of the
  plan somebody reviewed. If Odoo moved since that review, the apply is refused and recorded as
  REFUSED. `--auto-approve` skips the hash, for a script that has nobody to ask.
- **Holding the lock:**
  - the lock is held only while an apply runs;
  - a second apply meanwhile is refused (SPEC_LOCKED), and the refusal says who holds the lock and
    since when;
  - if an apply died, `spec unlock --lock-id` removes the lock, but only that lock.

## Who approves

The backend records **who** did something as the operating-system user and host name. That is a
label: odoo-cli authenticates nobody, and the bucket's permissions decide who can write there.
`odoo-cli trust` says so in its `specBackend` entry.

Here are two ways to make an approval come from a checked identity.

### 1. Your git host's reviews

Keep the spec in a repository. Run the plan on every pull request, and run the apply behind a
protected environment whose required reviewers are the people who approve.

`.github/workflows/odoo-spec-plan.yml`:

```yaml
name: odoo spec plan
on:
  pull_request:
    paths: ["specs/**"]
permissions:
  contents: read
  pull-requests: write
jobs:
  plan:
    runs-on: ubuntu-latest
    environment: odoo-plan          # an Odoo key that may read, and the bucket's keys
    env:
      AERP_CREDENTIAL_COMPANY: ${{ secrets.ODOO_KEY }}
      AWS_ACCESS_KEY_ID: ${{ secrets.SPEC_BUCKET_KEY_ID }}
      AWS_SECRET_ACCESS_KEY: ${{ secrets.SPEC_BUCKET_SECRET }}
    steps:
      - uses: actions/checkout@v4
      - run: curl -fsSL https://raw.githubusercontent.com/asao-vn/asao-odoo-tools/main/scripts/install.sh | sh
      - run: odoo-cli profile set --name company --endpoint "${{ vars.ODOO_URL }}" --database "${{ vars.ODOO_DB }}" --login "${{ vars.ODOO_LOGIN }}" --protocol json2
      - id: plan
        run: |
          odoo-cli spec plan --profile company --file specs/company.yaml --output json > plan.json
          echo "hash=$(jq -r .data.planHash plan.json)" >> "$GITHUB_OUTPUT"
          jq -r '.data.summary | to_entries | map("\(.key): \(.value)") | join(", ")' plan.json > summary.txt
      - uses: actions/github-script@v7
        with:
          script: |
            const fs = require('fs');
            const body = `Plan \`${{ steps.plan.outputs.hash }}\`: ${fs.readFileSync('summary.txt', 'utf8').trim()}\n\n` +
              'Once merged, an approver runs **odoo spec apply** with this hash.';
            github.rest.issues.createComment({...context.repo, issue_number: context.issue.number, body});
```

`.github/workflows/odoo-spec-apply.yml`:

```yaml
name: odoo spec apply
on:
  workflow_dispatch:
    inputs:
      plan_hash:
        description: "The planHash the pull request showed"
        required: true
jobs:
  apply:
    runs-on: ubuntu-latest
    environment: odoo-production     # required reviewers: their approval is the approval
    env:
      AERP_CREDENTIAL_COMPANY: ${{ secrets.ODOO_KEY }}
      AWS_ACCESS_KEY_ID: ${{ secrets.SPEC_BUCKET_KEY_ID }}
      AWS_SECRET_ACCESS_KEY: ${{ secrets.SPEC_BUCKET_SECRET }}
    steps:
      - uses: actions/checkout@v4
        with: {ref: main}
      - run: curl -fsSL https://raw.githubusercontent.com/asao-vn/asao-odoo-tools/main/scripts/install.sh | sh
      - run: odoo-cli profile set --name company --endpoint "${{ vars.ODOO_URL }}" --database "${{ vars.ODOO_DB }}" --login "${{ vars.ODOO_LOGIN }}" --protocol json2
      - run: odoo-cli spec apply --profile company --file specs/company.yaml --plan-hash "${{ inputs.plan_hash }}" --output json
```

The spec names its backend (`backend: s3://...`), so the workflows do not repeat it.

What this guarantees:
- GitHub records who approved the deployment;
- the backend's lock keeps two applies apart;
- a plan that changed after the review is refused.

The commands in these workflows are the ones verified against MinIO and an Odoo 19 lab. The workflow
files themselves have not been run on GitHub yet.

### 2. ASAO, when the team has ASAO accounts

ASAO's governed CLI holds the approval itself: `asao aerp spec plan`, `request`, `approve`, `apply`.
- The approver is a member holding `aerp.approval.decide`, and is never the requester.
- Every step needs the requester's own capability.
- The apply runs only if Odoo has not moved since the approval.
- Every step is in ASAO's audit trail.

That is the governed product; odoo-cli does not imitate it.

## Tiếng Việt

Khi nhiều người, script hay AI agent cùng làm trên một Odoo bằng `odoo-cli spec`, **backend** là chỗ
chung: một bucket S3 (hoặc R2, MinIO, B2) hay một thư mục dùng chung. Backend giữ:
- **khoá:** mỗi lúc chỉ một lần apply;
- **plan đã lưu:** theo mã hash, kèm nguyên văn spec, để người khác xem lại;
- **lịch sử:** mọi lần apply, bị từ chối, lỗi hay thành công;
- **spec đã apply gần nhất:** ai cũng kéo về được.

Không có tệp state như Terraform: mỗi lần plan đều đọc thẳng Odoo.

- `spec plan --backend s3://bucket/thu-muc`: lập plan và lưu lên backend.
- `spec apply --plan-hash ...`: apply đúng plan đã xem. Nếu Odoo đã đổi so với lúc xem, lệnh từ chối.
- `spec state`, `spec runs`, `spec pull`, `spec unlock`: xem trạng thái và lịch sử, kéo spec về, mở khoá
  khi một lần apply bị chết giữa chừng.

**Ai duyệt.** Backend chỉ ghi tên người dùng và tên máy, không xác thực ai cả. Muốn việc duyệt gắn với
một danh tính được kiểm tra thì có hai cách:
- dùng review của GitHub hay GitLab, như hai workflow ở trên: môi trường `odoo-production` có người duyệt
  bắt buộc;
- nếu nhóm có tài khoản ASAO: `asao aerp spec request`, `approve`, `apply`. Người duyệt phải khác người
  xin, và mỗi bước nằm trong nhật ký kiểm toán của ASAO.
