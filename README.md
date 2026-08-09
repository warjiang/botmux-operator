# botmux-operator

`botmux-operator` provisions one isolated [botmux](https://github.com/warjiang/botmux)
runtime per Kubernetes user. Each `BotmuxUser` owns one Feishu/Lark application,
one single-replica StatefulSet and one persistent RWO volume.

## Architecture

Each user Pod contains:

- a botmux daemon connected to that user's Feishu/Lark application;
- a dashboard container serving port `7891` and proxying `/s/*` Web Terminal traffic;
- an init container that merges operator-owned fields into the writable
  `$HOME/.botmux/bots.json` without removing botmux runtime state.

The user's PVC contains botmux sessions, CLI homes and `/workspace`. Deleting a
`BotmuxUser` retains the PVC by default.

## Install

```bash
make manifests
kustomize build config/default | kubectl apply -f -
```

Create the Lark secret and optional provider secret, then apply
[`config/samples/botmux_v1alpha1_botmuxuser.yaml`](config/samples/botmux_v1alpha1_botmuxuser.yaml).

```bash
kubectl get botmuxusers
kubectl describe botmuxuser alice
```

The Lark credentials Secret must contain `appSecret`. Secrets listed under
`spec.runtime.envFromSecretRefs` are injected only into the daemon container.

## Runtime images

The built-in catalog maps:

| `cliId` | Default image |
| --- | --- |
| `codex` | `ghcr.io/warjiang/botmux-runtime-codex:v0.1.0` |
| `claude-code` | `ghcr.io/warjiang/botmux-runtime-claude:v0.1.0` |
| `gemini` | `ghcr.io/warjiang/botmux-runtime-gemini:v0.1.0` |

Set `spec.runtime.image` for a custom image. A compatible image must contain
Node.js, tmux, botmux at `/opt/botmux`, and the selected CLI. Runtime images are
built with explicit botmux and CLI version inputs; containers never download
CLIs during startup.

## Storage lifecycle

- `Retain` (default): deleting the CR removes compute/network resources and
  leaves the PVC for recovery.
- `Delete`: the finalizer first stops the StatefulSet and then removes the PVC.
- `spec.suspend: true`: scales the StatefulSet to zero while retaining all data.

Changing `spec.lark.appId` is rejected. Secret rotation is supported and causes
a controlled single-replica StatefulSet restart.

## Development

```bash
make test
make build
```

Run the Kind end-to-end suite with:

```bash
test/e2e/kind.sh
```
