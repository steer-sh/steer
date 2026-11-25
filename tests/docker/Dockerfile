# A fresh Linux host with Claude Code, Codex and Pi installed and no Steer state.
# tests/fresh_install.py builds it and runs the README's install commands in it.
# Pi needs Node.js 22.19 or later, which Debian's own package does not provide.
FROM node:22-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
      python3 git ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
RUN npm install -g @openai/codex @earendil-works/pi-coding-agent
RUN useradd -m -s /bin/bash runner

USER runner
WORKDIR /home/runner
RUN curl -fsSL https://claude.ai/install.sh | bash
ENV PATH=/home/runner/.local/bin:$PATH

CMD ["bash"]
