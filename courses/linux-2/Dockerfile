FROM alpine:latest

# System dependencies (minimal set)
RUN apk add --no-cache \
    git \
    openssh \
    ncurses \
    ncurses-terminfo \
    ncurses-terminfo-base

# Create non-root user first
RUN addgroup -g 1000 termuser && \
    adduser -D -u 1000 -G termuser termuser -h /home/termuser

# Create our restricted environment structure
RUN mkdir -p /usr/local/bin

# Copy allowed executables and secure system binary locations
RUN for cmd in sh sed base64 date ps readlink tr stty cat terminfo cd chmod cp echo grep git ssh-keygen head id less ls md5sum cut sed sleep mkdir mv pwd rm tail touch vi vim tput clear wc which whoami; do \
        if [ -f "/bin/$cmd" ]; then \
            cp "/bin/$cmd" "/usr/local/bin/"; \
        elif [ -f "/usr/bin/$cmd" ]; then \
            cp "/usr/bin/$cmd" "/usr/local/bin/"; \
        fi; \
    done

# Set up lab environment: git signing repo directly in home
RUN git clone https://github.com/jonasbg/linux-git-signing-repo /home/termuser/git-signing && \
    chown -R termuser:termuser /home/termuser/git-signing

# Configure shell environment
COPY --chown=root:root <<-'EOF' /home/termuser/.profile
# Set restricted PATH
export PATH=/usr/local/bin 2>/dev/null

# Pure-inspired prompt
PS1='\n\033[1;34m$(pwd | sed "s|^$HOME/|~/|; s|^$HOME$||" 2>/dev/null)\033[0m\n\033[1;32m❯\033[0m '

# Additional color settings for terminal commands
export LS_COLORS='di=1;34:ln=1;36:so=1;35:pi=1;33:ex=1;32:bd=1;33:cd=1;33:su=1;31:sg=1;31:tw=1;34:ow=1;34'

# Enable color support
alias ls='/usr/local/bin/ls --color=auto'
alias grep='/usr/local/bin/grep --color=always'

# Explicitly set grep colors
export GREP_COLORS='ms=01;31:mc=01;31:sl=:cx=:fn=01;32:ln=01;36:bn=01;33:se=01;35'

# Ensure color terminal is properly set
export TERM=xterm-256color

# Environment variables
export LANG=nb_NO.UTF-8
export EDITOR=/usr/local/bin/vim
export HOME=/home/termuser
export LESS=-FRX

readonly PATH

# Basic aliases with full paths
alias ll='/usr/local/bin/ls -la'
alias l='/usr/local/bin/ls -l'
alias ..='cd ..'
alias cls='/usr/local/bin/clear'

EOF

# Make profile file read-only
RUN chmod 444 /home/termuser/.profile

# Secure system binaries (after all setup is complete)
RUN chown -R root:root /bin /usr/bin /sbin /usr/sbin && \
    chmod -R 644 /bin/* /usr/bin/* /sbin/* /usr/sbin/* && \
    chmod 555 /bin /usr/bin /sbin /usr/sbin

# Switch to termuser
USER termuser
WORKDIR /home/termuser

# Force sh to use our profile
ENV ENV=/home/termuser/.profile \
    SHELL=/usr/local/bin/sh

CMD ["/usr/local/bin/sh", "-l"]
