# A fresh Linux host with Claude Code and Codex installed and no Steer state.
# tests/fresh_install.py builds it and runs the README's install commands in it.
FROM debian:bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
      python3 git ca-certificates curl nodejs npm \
    && rm -rf /var/lib/apt/lists/*
RUN npm install -g @openai/codex
RUN useradd -m -s /bin/bash runner

USER runner
WORKDIR /home/runner
RUN curl -fsSL https://claude.ai/install.sh | bash
ENV PATH=/home/runner/.local/bin:$PATH

CMD ["bash"]
