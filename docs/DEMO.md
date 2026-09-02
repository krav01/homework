# Reviewer demonstration

This is the order I would use in a review. It keeps the demonstration focused
on observable behavior first and leaves the architecture discussion until the
requirements have been proven. The automated version is `make e2e`.

## 1. Create the environment

Prerequisites: Docker, kind, and kubectl.

```bash
./requirements.sh
kubectl get eps -n sunday-system
kubectl get pods -n sunday-system
```

Expected custom-resource columns:

```text
NAME         AGE   RESTARTS
sunday-app   ...   0
```

The exact age and restart count depend on the current cluster.

## 2. Exercise the API

Keep this command running in one terminal:

```bash
kubectl port-forward -n sunday-system service/sunday-app 8080:80
```

In another terminal:

```bash
curl -sS -X POST http://127.0.0.1:8080/write \
  -H 'Content-Type: application/json' \
  -d '{"user_id":"loki","product_name":"apple","amount":2}'

curl -sS \
  'http://127.0.0.1:8080/get_product_amount?product_name=apple'

curl -i -X DELETE \
  'http://127.0.0.1:8080/delete_product?product_name=apple'
```

This demonstrates the requested POST, GET, and DELETE operations. Names are
restricted to lowercase ASCII letters and amounts must be positive integers.

## 3. Demonstrate deletion recovery and persistence

Write a value, then delete the managed application Pod:

```bash
curl -sS -X POST http://127.0.0.1:8080/write \
  -H 'Content-Type: application/json' \
  -d '{"user_id":"thor","product_name":"yogurt","amount":3}'

old_pod=$(kubectl get etherealpod sunday-app -n sunday-system \
  -o jsonpath='{.status.podName}')
kubectl delete pod "$old_pod" -n sunday-system
kubectl get pods -n sunday-system -w
```

After the replacement is Ready, restart port-forwarding if it disconnected and
read the value again:

```bash
curl -sS \
  'http://127.0.0.1:8080/get_product_amount?product_name=yogurt'
```

The amount remains `3` because storage is attached through the PVC rather than
the Pod filesystem.

## 4. Demonstrate crash recovery and restart reporting

The automated E2E test creates a second EtherealPod whose container exits
immediately, then waits for the kubelet to restart it and for the controller to
publish the count:

```bash
make e2e
kubectl get eps -n sunday-system
```

The E2E script cleans up its crashing resource before it exits.

### Replay a recorded run

Every **Verify Sunday System** run produces a **sunday-system-demo** artifact.
Download it from the run's summary and open `sunday-demo.html` in a browser.
This is a recording of real test output, not a simulated terminal. The header
identifies the recorded commit and timestamp, and failed runs are marked FAILED.
Recordings are retained for 30 days; download one to keep it longer.

To record a fresh local deployment check:

```bash
python3 tools/record_demo.py --output work/sunday-demo -- ./e2e/e2e.sh
```

The `.cast` file can also be replayed with `asciinema play work/sunday-demo.cast`.

## 5. Demonstrate a template rollout

Change a template annotation and watch the old Pod terminate before a new Pod
appears. A service interruption during this serial rollout is expected.

```bash
kubectl patch etherealpod sunday-app -n sunday-system --type merge \
  -p '{"spec":{"template":{"metadata":{"annotations":{"demo":"rollout-v2"}}}}}'
kubectl get pods -n sunday-system -w
```

Once the replacement is Ready, read the saved product again. `make e2e` also
checks this sequence automatically.

## 6. Explain the design

The points I would call out are:

- owner references define managed Pods; labels do not establish ownership;
- the kubelet handles container crashes, while the controller handles Pod
  deletion and terminal phases;
- direct Pod ownership keeps restart reporting unambiguous;
- atomic JSON on a PVC with a lifetime file lock enforces one writer, while a
  database would be the next step for multiple replicas or large datasets;
- controller-runtime supplies watches, reconciliation, leader election, and
  health endpoints without reimplementing Kubernetes plumbing;
- “exactly one” is an eventual convergence property during scheduling and
  termination, not an impossible promise of zero transition time.

Further rationale and alternatives are documented in
[`ARCHITECTURE.md`](ARCHITECTURE.md).
