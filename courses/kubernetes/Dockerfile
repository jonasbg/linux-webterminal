FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod main.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o kube-mock .

FROM alpine:latest

# System dependencies
RUN apk add --no-cache \
    ncurses \
    ncurses-terminfo \
    ncurses-terminfo-base \
    curl

# Download real kubectl binary (arch-aware)
RUN ARCH=$(uname -m) && \
    case "$ARCH" in \
        x86_64)  K8S_ARCH="amd64" ;; \
        aarch64) K8S_ARCH="arm64" ;; \
    esac && \
    curl -LO "https://dl.k8s.io/release/v1.31.0/bin/linux/${K8S_ARCH}/kubectl" && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin/kubectl

# Create non-root user
RUN addgroup -g 1000 termuser && \
    adduser -D -u 1000 -G termuser termuser -h /home/termuser

# Create restricted environment structure
RUN mkdir -p /usr/local/bin

# Copy allowed executables
RUN for cmd in sh sed base64 date ps readlink tr stty cat terminfo cd chmod cp echo grep head id less ls md5sum cut sleep mkdir mv pwd rm tail touch vi vim tput clear wc which whoami; do \
        if [ -f "/bin/$cmd" ]; then \
            cp "/bin/$cmd" "/usr/local/bin/"; \
        elif [ -f "/usr/bin/$cmd" ]; then \
            cp "/usr/bin/$cmd" "/usr/local/bin/"; \
        fi; \
    done

# Copy mock API server
COPY --from=builder /app/kube-mock /usr/local/bin/kube-mock

# Copy entrypoint and kubeconfig
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# Copy course materials
COPY instruction.md /tmp/instruction.md
COPY kubeconfig.yaml /tmp/kubeconfig.yaml
RUN mkdir -p /home/termuser/.kube && \
    mv /tmp/instruction.md /home/termuser/instruction.md && \
    mv /tmp/kubeconfig.yaml /home/termuser/.kube/config && \
    chown -R termuser:termuser /home/termuser

# Configure shell environment
COPY --chown=root:root <<-'EOF' /home/termuser/.profile
export PATH=/usr/local/bin 2>/dev/null
PS1='\n\033[1;34m$(pwd | sed "s|^$HOME/|~/|; s|^$HOME$||" 2>/dev/null)\033[0m\n\033[1;32m❯\033[0m '
export LS_COLORS='di=1;34:ln=1;36:so=1;35:pi=1;33:ex=1;32:bd=1;33:cd=1;33:su=1;31:sg=1;31:tw=1;34:ow=1;34'
alias ls='/usr/local/bin/ls --color=auto'
alias grep='/usr/local/bin/grep --color=always'
export GREP_COLORS='ms=01;31:mc=01;31:sl=:cx=:fn=01;32:ln=01;36:bn=01;33:se=01;35'
export TERM=xterm-256color
export LANG=nb_NO.UTF-8
export EDITOR=/usr/local/bin/vim
export HOME=/home/termuser
export LESS=-FRX
export KUBECONFIG=/home/termuser/.kube/config
readonly PATH
alias ll='/usr/local/bin/ls -la'
alias l='/usr/local/bin/ls -l'
alias ..='cd ..'
alias cls='/usr/local/bin/clear'
alias k='kubectl'
EOF

RUN chmod 444 /home/termuser/.profile

# Secure system binaries
RUN chown -R root:root /bin /usr/bin /sbin /usr/sbin && \
    chmod -R 644 /bin/* /usr/bin/* /sbin/* /usr/sbin/* && \
    chmod 555 /bin /usr/bin /sbin /usr/sbin

USER termuser
WORKDIR /home/termuser
ENV ENV=/home/termuser/.profile \
    SHELL=/usr/local/bin/sh \
    KUBECONFIG=/home/termuser/.kube/config

CMD ["/usr/local/bin/entrypoint.sh"]
