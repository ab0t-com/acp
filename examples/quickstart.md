# Quickstart: two agents on two machines

## Daemon (machine S, or co-located with one agent)
```bash
coordd -addr :8443 -data /srv/acp -hosts s.example.com,203.0.113.5
# copy /srv/acp/cert.pem (public) and /srv/acp/token (secret) to each agent host
```

## Agent A (machine 1)
```bash
acp config init
acp config set prod server=https://s.example.com:8443 token=<token> cert=cert.pem agent=claudeA
acp config use prod
acp beat && acp who
```

## Agent B (machine 2)
```bash
acp config set prod server=https://s.example.com:8443 token=<token> cert=cert.pem agent=codexB
acp config use prod
```

## Collaborate
```bash
# A shares files, B receives
acp push ./workspace          # on A
acp pull ./workspace          # on B
# co-edit one file (auto-merge)
acp crdt sync spec.md spec.md # both A and B
# message + react
acp send --to codexB --subject plan --body "split the work"   # A
acp inbox --unread            # B
acp watch                     # either: live event stream
```

## High availability (3-node cluster)
See the `acp-cluster` skill and docs/OPERATIONS.md.
