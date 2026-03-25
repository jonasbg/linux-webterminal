FROM docker.io/library/golang:1.26-alpine AS builder
WORKDIR /app
COPY kubernetes/go.mod ./go.mod
COPY kubernetes/main.go ./main.go
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o kube-mock .

FROM alpine:latest

RUN apk add --no-cache \
    ncurses \
    ncurses-terminfo \
    ncurses-terminfo-base \
    curl

RUN ARCH=$(uname -m) && \
    case "$ARCH" in \
        x86_64)  K8S_ARCH="amd64" ;; \
        aarch64) K8S_ARCH="arm64" ;; \
    esac && \
    curl -LO "https://dl.k8s.io/release/v1.31.0/bin/linux/${K8S_ARCH}/kubectl" && \
    chmod +x kubectl && \
    mv kubectl /usr/local/bin/kubectl

RUN addgroup -g 1000 termuser && \
    adduser -D -u 1000 -G termuser termuser -h /home/termuser

RUN mkdir -p /usr/local/bin

RUN for cmd in sh sed stty cat cd cp echo grep head less ls base64 cut sleep pwd mkdir rm tail vi tput clear wc which curl printenv env; do \
        if [ -f "/bin/$cmd" ]; then \
            cp "/bin/$cmd" "/usr/local/bin/"; \
        elif [ -f "/usr/bin/$cmd" ]; then \
            cp "/usr/bin/$cmd" "/usr/local/bin/"; \
        fi; \
    done

RUN cp /bin/busybox /usr/local/bin/busybox
COPY kubernetes/cat-wrapper /usr/local/bin/cat
RUN chmod +x /usr/local/bin/cat

COPY --from=builder /app/kube-mock /usr/local/bin/kube-mock
COPY kubernetes-networking/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

COPY kubernetes-networking/instruction.md /tmp/instruction.md
COPY kubernetes/kubeconfig.yaml /tmp/kubeconfig.yaml
COPY kubernetes-networking/seed.json /etc/kubernetes/mock-seed.json
RUN mkdir -p /home/termuser/.kube && \
    mv /tmp/instruction.md /home/termuser/instruction.md && \
    mv /tmp/kubeconfig.yaml /home/termuser/.kube/config && \
    chown -R termuser:termuser /home/termuser

COPY --chown=root:root <<-'EOF' /home/termuser/.profile
export PATH=/usr/local/bin 2>/dev/null
PS1='\n\033[1;34m$(pwd | sed "s|^$HOME/|~/|; s|^$HOME$||" 2>/dev/null)\033[0m\n\033[1;32m❯\033[0m '
export LS_COLORS='di=1;34:ln=1;36:so=1;35:pi=1;33:ex=1;32:bd=1;33:cd=1;33:su=1;31:sg=1;31:tw=1;34:ow=1;34'
alias ls='/usr/local/bin/ls --color=auto'
alias grep='/usr/local/bin/grep --color=always'
export GREP_COLORS='ms=01;31:mc=01;31:sl=:cx=:fn=01;32:ln=01;36:bn=01;33:se=01;35'
export TERM=xterm-256color
export LANG=nb_NO.UTF-8
export EDITOR=/usr/local/bin/vi
export KUBE_EDITOR=/usr/local/bin/vi
export HOME=/home/termuser
export LESS=-FRX
export KUBECONFIG=/home/termuser/.kube/config
readonly PATH
alias ll='/usr/local/bin/ls -la'
alias l='/usr/local/bin/ls -l'
alias ..='cd ..'
alias cls='/usr/local/bin/clear'
alias k='kubectl'
alias kedit='kubectl edit --validate=false'
alias vim='vi'
EOF

RUN chmod 444 /home/termuser/.profile

USER termuser
WORKDIR /home/termuser
ENV ENV=/home/termuser/.profile \
    SHELL=/usr/local/bin/sh \
    KUBECONFIG=/home/termuser/.kube/config

CMD ["/usr/local/bin/entrypoint.sh"]
