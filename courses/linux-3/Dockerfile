FROM alpine:latest

# System dependencies (minimal set)
RUN apk add --no-cache \
    ncurses \
    ncurses-terminfo \
    ncurses-terminfo-base

# Create non-root user first
RUN addgroup -g 1000 termuser && \
    adduser -D -u 1000 -G termuser termuser -h /home/termuser

# Create our restricted environment structure
RUN mkdir -p /usr/local/bin

# Copy allowed executables and secure system binary locations
# ps, sleep, kill, readlink needed for process investigation; tr for /proc parsing
RUN for cmd in sh sed stty cat cd cp echo grep head id less ls cut sleep pwd rm tail tr readlink ps kill tput clear wc which; do \
        if [ -f "/bin/$cmd" ]; then \
            cp "/bin/$cmd" "/usr/local/bin/"; \
        elif [ -f "/usr/bin/$cmd" ]; then \
            cp "/usr/bin/$cmd" "/usr/local/bin/"; \
        fi; \
    done

# Install markdown-aware cat wrapper
RUN cp /bin/busybox /usr/local/bin/busybox
COPY cat-wrapper /usr/local/bin/cat
RUN chmod +x /usr/local/bin/cat

# Set up lab environment: process investigation task
COPY instructions.md /tmp/instruction.md
RUN mv /tmp/instruction.md /home/termuser/instruction.md && \
    chown termuser:termuser /home/termuser/instruction.md

# Configure shell environment
COPY --chown=root:root <<-'EOF' /home/termuser/.profile
# Set restricted PATH
export PATH=/usr/local/bin 2>/dev/null

# Pure-inspired prompt
PS1='\n\033[1;34m$(pwd | sed "s|^$HOME/|~/|; s|^$HOME$||" 2>/dev/null)\033[0m\n\033[1;32m❯\033[0m '

# Additional color settings for terminal commands
export LS_COLORS='di=1;34:ln=1;36:so=1;35:pi=1;33:ex=1;32:bd=1;33:cd=1;33:su=1;31:sg=1;31:tw=1:ow=1;34'

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


# Switch to termuser
USER termuser
WORKDIR /home/termuser

# Force sh to use our profile
ENV ENV=/home/termuser/.profile \
    SHELL=/usr/local/bin/sh

CMD ["/usr/local/bin/sh", "-l"]
