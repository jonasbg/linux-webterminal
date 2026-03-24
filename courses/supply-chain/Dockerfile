FROM docker.io/aquasec/trivy:0.69.3 AS trivy

FROM alpine:latest

RUN apk add --no-cache \
    git \
    openssh \
    jq \
    ncurses \
    ncurses-terminfo \
    ncurses-terminfo-base \
    podman \
    shadow shadow-uidmap

RUN addgroup -g 1000 termuser && \
    adduser -D -u 1000 -G termuser termuser -h /home/termuser

RUN echo 'termuser:100000:65536' >> /etc/subuid && \
    echo 'termuser:100000:65536' >> /etc/subgid

RUN mkdir -p /usr/local/bin /home/termuser/.local/share/containers/storage && \
    chown -R termuser:termuser /home/termuser/.local && \
    chmod -R 755 /home/termuser/.local

RUN for cmd in sh conmon crun newuidmap newgidmap sed base64 date ps readlink tr stty cat terminfo cd chmod cp echo grep git ssh-keygen head id less ls md5sum cut sleep mkdir mv pwd rm tail touch vi vim tput clear wc which whoami podman jq printenv env; do \
        if [ -f "/bin/$cmd" ]; then \
            cp "/bin/$cmd" "/usr/local/bin/"; \
        elif [ -f "/usr/bin/$cmd" ]; then \
            cp "/usr/bin/$cmd" "/usr/local/bin/"; \
        fi; \
    done && \
    chmod u+s /usr/local/bin/newuidmap /usr/local/bin/newgidmap

RUN cp /bin/busybox /usr/local/bin/busybox
COPY cat-wrapper /usr/local/bin/cat
RUN chmod +x /usr/local/bin/cat

COPY --from=trivy /usr/local/bin/trivy /usr/local/bin/trivy

COPY instruction.md /home/termuser/instruction.md
COPY insecure.Dockerfile /home/termuser/insecure.Dockerfile
COPY hardened.Dockerfile /home/termuser/hardened.Dockerfile
COPY policy.sh /home/termuser/policy.sh
RUN chown termuser:termuser /home/termuser/instruction.md /home/termuser/insecure.Dockerfile /home/termuser/hardened.Dockerfile /home/termuser/policy.sh && \
    chmod 755 /home/termuser/policy.sh

USER termuser
RUN mkdir -p ~/.config/containers && \
    echo -e "[storage]\ndriver = \"overlay\"" > ~/.config/containers/storage.conf

COPY --chown=root:root <<-'EOF' /home/termuser/.config/cni/net.d/87-podman-bridge.conflist
{
  "cniVersion": "0.4.0",
  "name": "podman",
  "plugins": [
    {
      "type": "bridge",
      "bridge": "cni-podman0",
      "isGateway": true,
      "ipMasq": true,
      "ipam": {
        "type": "host-local",
        "subnet": "10.88.0.0/16"
      }
    }
  ]
}
EOF

USER root

COPY <<-'EOF' /etc/containers/containers.conf
[engine]
runtime = "/usr/local/bin/crun"
conmon_path=["/usr/local/bin/conmon"]
pod_exit_policy="stop"

[containers]
netns="none"

EOF

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
export HOME=/home/termuser
export LESS=-FRX
readonly PATH
alias ll='/usr/local/bin/ls -la'
alias l='/usr/local/bin/ls -l'
alias ..='cd ..'
alias cls='/usr/local/bin/clear'
alias docker="podman"
alias vim='vi'
EOF

RUN chmod 444 /home/termuser/.profile

RUN chown -R root:root /bin /usr/bin /sbin /usr/sbin && \
    chmod -R 644 /bin/* /usr/bin/* /sbin/* /usr/sbin/* && \
    chmod 555 /bin /usr/bin /sbin /usr/sbin

VOLUME [ "/run", "/var/lib/containers", "/tmp" ]

USER termuser
WORKDIR /home/termuser

ENV ENV=/home/termuser/.profile \
    SHELL=/usr/local/bin/sh

CMD ["/usr/local/bin/sh", "-l"]
