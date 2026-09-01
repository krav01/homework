# Sunday System

[![Verify Sunday System](https://github.com/krav01/homework/actions/workflows/verify.yml/badge.svg)](https://github.com/krav01/homework/actions/workflows/verify.yml)

My Go and Kubernetes home assignment: a custom controller that restores a
managed Pod after deletion, paired with a groceries API that keeps its data
across Pod replacements.

**Author:** [Vladimir Krauchuk (@krav01)](https://github.com/krav01)  
**Stack:** Go · Kubernetes · controller-runtime · Docker · kind · GitHub Actions

I split the project into two components:

- `EtherealPod`, a namespaced Kubernetes custom resource that maintains one
  active Pod and exposes its restart count through `kubectl get eps`.
- `SundayApp`, a small groceries API whose data is atomically persisted on a
  PersistentVolumeClaim, so deleting or replacing its Pod does not lose data.

## A short note on my approach

I started with the failure cases rather than the HTTP endpoints: what happens
when a container exits, when the whole Pod disappears, and where the data lives
while that happens. That led to two separate responsibilities. Kubernetes
restarts a failed container in the same Pod, while the custom controller
replaces a Pod that is deleted or reaches a terminal phase.

I kept the solution small enough for a home assignment. I used the Go standard library where it was enough and introduced abstractions
only at boundaries that benefited from testing. The result is deliberately
small, but the tradeoffs and the next production steps are explicit.

## Architecture

The repository is one Go module with two small binaries:

```text
api/v1alpha1/       EtherealPod API types
cmd/controller/     controller-runtime composition root
cmd/sundayapp/      HTTP service composition root
internal/controller reconciliation logic
internal/httpapi/   HTTP transport and validation
internal/store/     atomic JSON file persistence
config/             CRD and RBAC resources
examples/           sample EtherealPod
e2e/                live-cluster acceptance check
docs/               architecture rationale and demo walkthrough
submission.yaml     deployable cluster resources
requirements.sh     repeatable local kind setup
.github/workflows/  remote Docker/kind verification
```

Dependencies are wired manually. The API depends on a narrow storage
interface, while the controller owns Kubernetes-specific reconciliation. I
kept these boundaries visible because they make the important behavior easy to
test without adding a large application framework.

Go and controller-runtime provide the native Kubernetes control loop; direct
Pod ownership keeps restart reporting unambiguous; atomic JSON persistence on
a PVC provides one-writer durability without operating a database. The
alternatives I considered, failure semantics, and production tradeoffs are
documented in
[`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md).

## Prerequisites and quick start

Install Docker, `kind`, and `kubectl`, then run:

```bash
git clone https://github.com/krav01/homework.git
cd homework
./requirements.sh
```

The script creates (or reuses) a `kind` cluster named `sunday-system`, builds
and loads both images, installs the CRD, and applies `submission.yaml`. That
manifest contains the controller, persistent storage, Service, and the
EtherealPod running SundayApp. Re-running the script rolls the controller and
application Pod so they use the newly built images while preserving API data.

Inspect it with:

```bash
kubectl get eps -n sunday-system
kubectl get pods -n sunday-system
kubectl port-forward -n sunday-system service/sunday-app 8080:80
```

## API

| Method | Endpoint | Behavior |
| --- | --- | --- |
| `POST` | `/write` | Add a quantity of a product for a user |
| `GET` | `/get_product_amount` | Sum a product's quantity across all users |
| `DELETE` | `/delete_product` | Remove a product from every user |


Names and product names must contain lowercase ASCII letters. Amounts must be
positive integers. `POST /write` accepts JSON, URL query parameters, or form
parameters because the assignment does not prescribe an encoding.

```bash
curl -sS -X POST http://127.0.0.1:8080/write \
  -H 'Content-Type: application/json' \
  -d '{"user_id":"loki","product_name":"apple","amount":1}'

curl -sS 'http://127.0.0.1:8080/get_product_amount?product_name=apple'

curl -i -X DELETE \
  'http://127.0.0.1:8080/delete_product?product_name=apple'
```

Product amounts are summed across all users. Deleting a product removes it
from every user. Missing products return amount `0` on GET and `404` on
DELETE.

## Self-healing demonstration

Delete the managed application Pod and watch the controller recreate it:

```bash
pod=$(kubectl get pod -n sunday-system \
  -l sunday.system/etherealpod=sunday-app \
  -o jsonpath='{.items[0].metadata.name}')
kubectl delete pod -n sunday-system "$pod"
kubectl get pods -n sunday-system -w
```

Write an item before deletion and read it after the replacement becomes Ready
to verify that PVC-backed data survived.

Container crashes are handled by the enforced `restartPolicy: Always`; the
controller reports the sum of init- and application-container restart counts
in `.status.restarts`. Terminal Pods and accidental duplicate Pods are removed,
and a replacement is created when no active managed Pod remains.

## Development checks

Local checks require Go 1.26.5 or a compatible newer toolchain, as declared in
`go.mod`, and `make`. Cluster deployment builds Go binaries inside Docker.

```bash
make build
make test
make test-race
make lint       # requires golangci-lint v2
make e2e        # requires a deployment created by ./requirements.sh
```

The E2E check writes data, deletes the application Pod, verifies that the
replacement can read the persisted value, then starts a deliberately crashing
EtherealPod and verifies that `RESTARTS` increases.

GitHub Actions runs the same acceptance check on an `ubuntu-latest` runner. It
builds both Docker images, creates a disposable kind cluster, deploys the
system, runs `make e2e`, and prints cluster diagnostics if anything fails.

For a short reviewer-facing walkthrough, including discussion points, follow
[`docs/DEMO.md`](docs/DEMO.md).

## Requirements traceability

| Assignment requirement | Implementation |
| --- | --- |
| Exactly one active Pod per EtherealPod | Owner-reference discovery plus deterministic duplicate cleanup in the reconciler |
| Recovery after a crash | Enforced `restartPolicy: Always`; terminal Pods are replaced |
| Recovery after Pod deletion | Owned-Pod watch triggers creation of a replacement |
| `NAME AGE RESTARTS` in `kubectl get eps` | CRD printer columns and status restart aggregation |
| Sunday data never disappears with its Pod | Atomic file persistence on a PersistentVolumeClaim |
| GET, POST, DELETE API | Typed routes, input validation, persistence, and HTTP tests |

## Assumptions

- “Exactly one running Pod” is implemented as exactly one non-terminal managed
  Pod during startup, because Kubernetes cannot make a replacement Running
  instantaneously. A Running Pod is preferred when duplicates are observed.
- The restart column reflects the current managed Pod, matching the wording of
  the assignment; deliberate Pod replacement starts a new Kubernetes restart
  counter.
- One SundayApp replica is managed by each EtherealPod. The RWO PVC and atomic
  file writes are therefore sufficient and avoid introducing an external
  database for this assignment.
- Kubernetes must establish a CRD before resources of its new kind can be
  decoded. For that reason `requirements.sh` installs the CRD first and then
  applies the self-contained workload resources in `submission.yaml`.
## Scope and next steps

I built this as a focused home assignment. The controller converges toward
one active Pod; recovery still takes time for reconciliation and scheduling.
The JSON store is intended for a single application writer. Existing Pods do
not automatically roll out when the custom resource's template changes.

For a production version, I would add controlled template rollouts,
authentication and rate limiting, backups, and a database when multiple
writers or larger datasets are required.

- [Architecture and design decisions](docs/ARCHITECTURE.md)
- [Step-by-step demonstration](docs/DEMO.md)
- [Automated verification](https://github.com/krav01/homework/actions)
