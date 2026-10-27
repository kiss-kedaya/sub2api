# Copilot SDK adapter

Enable this optional channel only after installing a dedicated adapter with a
GitHub account that has Copilot access. End users use ordinary site API keys.
They never receive the adapter authentication key or GitHub token.

The gateway preserves native Responses tools and session identifiers, forces
HTTP transport and propagates cancellation. A stateful turn is not replayed on
another account after an ambiguous failure. Start a new thread if the adapter
reports that a continuation is unavailable.

## Install the companion service

Use a clean checkout of this repository. The scripts live in
`deploy/copilot-sdk/`. `prepare.sh /absolute/adapter/path` checks out the pinned
public `Nonary/ghcp_proxy` adapter revision and installs the pinned SDK. It does
not log in or start a service. Review `sidecar.env.example` and the systemd unit
and use a dedicated unprivileged service account.

Run `login.py --state-dir /private/service/state` using the prepared adapter
virtual environment. Complete the displayed GitHub device authorization. State,
adapter authentication and signing keys are stored outside the repository.
Keep the state directory private to that service account. Configure the supplied
environment file and unit with those paths before starting it.

The default listener is loopback port 18765. A container's loopback is different
from its host's loopback: use an explicitly configured private network when the
gateway runs in another container or host. Limit access to authorized gateways.
Configure the gateway URL policy to allow this specific trusted adapter address.

## Configure an upstream account

Create an OpenAI API Key account, enter the adapter's `/v1` base URL and its
authentication key, and enable **GitHub Copilot SDK**. Use the model names
returned by that adapter. Give the account its own group and create a site key
for the group. Confirm pricing before offering the group to users. The mode is
disabled by default and does not authorize GitHub automatically.

The signing key must persist across adapter restarts. It protects tool
continuations and binds them to the site API key and conversation. Rotating it
invalidates existing continuations. In-memory active turns are not transferable
between adapter processes.

## Validation

The companion tests use synthetic SDK events and verify tenant isolation,
signed tool continuations, cancellation and compressed request limits. GitHub
CI runs these checks against the pinned adapter. They do not authorize or send
requests to GitHub on behalf of a real account. A real authorized account is
still required for provider acceptance.

Adapted from the public `ranxi2001/sub2api` implementation at commit 9620d315.
The separate adapter retains its own upstream license and attribution.
