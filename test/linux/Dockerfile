# Linux integration rig for the deckchat companion's input and focus
# packages: real /dev/uinput (needs `docker run --privileged`) and Xvfb as a
# stand-in for gamescope's Xwayland. See run.sh.
FROM golang:1.26-bookworm
RUN apt-get update \
 && apt-get install -y --no-install-recommends xvfb x11-utils xauth evtest \
 && rm -rf /var/lib/apt/lists/*
ENV GOTOOLCHAIN=local GOFLAGS=-buildvcs=false
COPY in-container.sh /usr/local/bin/deckchat-in-container
RUN chmod +x /usr/local/bin/deckchat-in-container
WORKDIR /src/companion
CMD ["deckchat-in-container"]
