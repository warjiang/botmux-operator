#!/usr/bin/env bash
set -euo pipefail

cluster_name="${KIND_CLUSTER_NAME:-botmux-operator-e2e}"
kind_node_image="${KIND_NODE_IMAGE:-kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5}"
operator_image="botmux-operator:e2e"
runtime_image="botmux-runtime:e2e"

cleanup() {
  status=$?
  trap - EXIT
  set +e
  if (( status != 0 )) && [[ -n "${E2E_ARTIFACT_DIR:-}" ]]; then
    mkdir -p "${E2E_ARTIFACT_DIR}"
    kubectl get pods,deployments,statefulsets,services,ingresses,persistentvolumeclaims,botmuxusers \
      --all-namespaces -o wide >"${E2E_ARTIFACT_DIR}/resources.txt" 2>&1
    kubectl get events --all-namespaces --sort-by=.lastTimestamp \
      >"${E2E_ARTIFACT_DIR}/events.txt" 2>&1
    kind export logs "${E2E_ARTIFACT_DIR}/kind" --name "${cluster_name}" \
      >"${E2E_ARTIFACT_DIR}/kind-export.log" 2>&1
  fi
  kind delete cluster --name "${cluster_name}" >/dev/null 2>&1 || true
  exit "${status}"
}
trap cleanup EXIT

docker build -t "${operator_image}" .
docker build -t "${runtime_image}" test/e2e/runtime
kind create cluster --name "${cluster_name}" --image "${kind_node_image}" --wait 120s
kind load docker-image --name "${cluster_name}" "${operator_image}" "${runtime_image}"

kustomize build config/default \
  | sed "s#ghcr.io/warjiang/botmux-operator:v0.1.0#${operator_image}#g" \
  | kubectl apply -f -
kubectl -n botmux-system rollout status deployment/controller-manager --timeout=180s

kubectl create namespace botmux-e2e
kubectl -n botmux-e2e create secret generic alice-lark --from-literal=appSecret=test-secret
kubectl -n botmux-e2e create secret generic alice-provider --from-literal=OPENAI_API_KEY=test-key
kubectl apply -f - <<EOF
apiVersion: botmux.io/v1alpha1
kind: BotmuxUser
metadata:
  name: alice
  namespace: botmux-e2e
spec:
  lark:
    appId: cli_e2e_alice
    credentialsSecretRef:
      name: alice-lark
  runtime:
    cliId: e2e
    image: ${runtime_image}
    envFromSecretRefs:
    - name: alice-provider
  workspace:
    size: 1Gi
    reclaimPolicy: Retain
EOF

kubectl -n botmux-e2e wait --for=condition=Ready botmuxuser/alice --timeout=180s
test "$(kubectl -n botmux-e2e get statefulset botmux-alice -o jsonpath='{.spec.replicas}')" = "1"
test "$(kubectl -n botmux-e2e get pods -l botmux.io/user=alice --no-headers | wc -l | tr -d ' ')" = "1"

kubectl -n botmux-e2e exec botmux-alice-0 -c daemon -- sh -ec 'printf persisted >/data/workspace/e2e-marker'
kubectl -n botmux-e2e delete pod botmux-alice-0 --wait=true
kubectl -n botmux-e2e wait --for=condition=Ready pod/botmux-alice-0 --timeout=180s
test "$(kubectl -n botmux-e2e exec botmux-alice-0 -c daemon -- cat /data/workspace/e2e-marker)" = "persisted"

old_revision="$(kubectl -n botmux-e2e get statefulset botmux-alice -o jsonpath='{.spec.template.metadata.annotations.botmux\\.io/credentials-revision}')"
kubectl -n botmux-e2e create secret generic alice-provider --from-literal=OPENAI_API_KEY=rotated --dry-run=client -o yaml | kubectl apply -f -
for _ in $(seq 1 60); do
  pod_count="$(kubectl -n botmux-e2e get pods -l botmux.io/user=alice --no-headers 2>/dev/null | wc -l | tr -d ' ')"
  if (( pod_count > 1 )); then
    echo "credential rotation created ${pod_count} alice Pods" >&2
    exit 1
  fi
  new_revision="$(kubectl -n botmux-e2e get statefulset botmux-alice -o jsonpath='{.spec.template.metadata.annotations.botmux\\.io/credentials-revision}')"
  if [[ "${new_revision}" != "${old_revision}" ]]; then
    break
  fi
  sleep 2
done
test "${new_revision}" != "${old_revision}"
kubectl -n botmux-e2e rollout status statefulset/botmux-alice --timeout=180s

openssl req -x509 -newkey rsa:2048 -nodes -subj /CN=alice.example.test -days 1 \
  -keyout /tmp/alice.key -out /tmp/alice.crt 2>/dev/null
kubectl -n botmux-e2e create secret tls alice-tls --cert=/tmp/alice.crt --key=/tmp/alice.key
kubectl -n botmux-e2e patch botmuxuser alice --type=merge \
  -p '{"spec":{"ingress":{"enabled":true,"host":"alice.example.test","tlsSecretName":"alice-tls"}}}'
for _ in $(seq 1 30); do
  kubectl -n botmux-e2e get ingress botmux-alice >/dev/null 2>&1 && break
  sleep 2
done
test "$(kubectl -n botmux-e2e get ingress botmux-alice -o jsonpath='{.spec.rules[0].host}')" = "alice.example.test"
test "$(kubectl -n botmux-e2e get ingress botmux-alice -o jsonpath='{.spec.tls[0].secretName}')" = "alice-tls"
test "$(kubectl -n botmux-e2e get ingress botmux-alice -o jsonpath='{.spec.rules[0].http.paths[0].path}')" = "/"
test "$(kubectl -n botmux-e2e get botmuxuser alice -o jsonpath='{.status.dashboardURL}')" = "https://alice.example.test"

kubectl -n botmux-e2e port-forward service/botmux-alice 17891:80 >/tmp/botmux-e2e-port-forward.log 2>&1 &
port_forward_pid=$!
for _ in $(seq 1 30); do
  curl -fsS http://127.0.0.1:17891/__health >/dev/null 2>&1 && break
  sleep 1
done
websocket_status="$(curl --http1.1 -sS -o /dev/null -w '%{http_code}' \
  -H 'Connection: Upgrade' -H 'Upgrade: websocket' \
  -H 'Sec-WebSocket-Version: 13' -H 'Sec-WebSocket-Key: ZTItdGVzdC1rZXk=' \
  http://127.0.0.1:17891/s/e2e)"
kill "${port_forward_pid}" >/dev/null 2>&1 || true
wait "${port_forward_pid}" 2>/dev/null || true
test "${websocket_status}" = "101"

if kubectl -n botmux-e2e get botmuxuser,statefulset,service,ingress -o yaml | grep -E 'test-secret|test-key|rotated'; then
  echo "Secret value leaked into Kubernetes resources" >&2
  exit 1
fi
if for pod in $(kubectl -n botmux-system get pods -l control-plane=controller-manager -o name); do
  kubectl -n botmux-system logs "${pod}" --all-containers --prefix
done | grep -E 'test-secret|test-key|rotated'; then
  echo "Secret value leaked into operator logs" >&2
  exit 1
fi

kubectl -n botmux-e2e patch botmuxuser alice --type=merge -p '{"spec":{"suspend":true}}'
for _ in $(seq 1 60); do
  replicas="$(kubectl -n botmux-e2e get statefulset botmux-alice -o jsonpath='{.spec.replicas}')"
  [[ "${replicas}" = "0" ]] && break
  sleep 2
done
test "${replicas}" = "0"

kubectl -n botmux-e2e create secret generic sandbox-lark --from-literal=appSecret=test-secret
kubectl apply -f - <<EOF
apiVersion: botmux.io/v1alpha1
kind: BotmuxUser
metadata:
  name: sandbox
  namespace: botmux-e2e
spec:
  lark:
    appId: cli_e2e_sandbox
    credentialsSecretRef:
      name: sandbox-lark
  runtime:
    cliId: e2e
    image: ${runtime_image}
    sandbox: true
  workspace:
    size: 1Gi
    reclaimPolicy: Delete
EOF
for _ in $(seq 1 90); do
  sandbox_reason="$(kubectl -n botmux-e2e get botmuxuser sandbox -o jsonpath='{.status.conditions[?(@.type=="WorkloadReady")].reason}')"
  [[ "${sandbox_reason}" = "SandboxUnsupported" ]] && break
  sleep 2
done
test "${sandbox_reason}" = "SandboxUnsupported"
kubectl -n botmux-e2e delete botmuxuser sandbox --wait=true

kubectl -n botmux-e2e delete botmuxuser alice --wait=true
kubectl -n botmux-e2e get pvc botmux-alice >/dev/null

kubectl -n botmux-e2e create secret generic bob-lark --from-literal=appSecret=test-secret
kubectl apply -f - <<EOF
apiVersion: botmux.io/v1alpha1
kind: BotmuxUser
metadata:
  name: bob
  namespace: botmux-e2e
spec:
  lark:
    appId: cli_e2e_bob
    credentialsSecretRef:
      name: bob-lark
  runtime:
    cliId: e2e
    image: ${runtime_image}
  workspace:
    size: 1Gi
    reclaimPolicy: Delete
EOF
kubectl -n botmux-e2e wait --for=condition=Ready botmuxuser/bob --timeout=180s
kubectl -n botmux-e2e delete botmuxuser bob --wait=true
if kubectl -n botmux-e2e get pvc botmux-bob >/dev/null 2>&1; then
  echo "Delete reclaim policy left PVC botmux-bob behind" >&2
  exit 1
fi
