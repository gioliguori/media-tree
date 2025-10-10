FROM debian:12-slim

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    git build-essential autoconf automake libtool pkg-config \
    **libsoup-3.0-dev** \
    libglib2.0-dev libgstreamer1.0-dev \
    **libgstreamer-plugins-base1.0-dev** libgstreamer-plugins-bad1.0-dev \
    gstreamer1.0-tools gstreamer1.0-nice \
    gstreamer1.0-plugins-bad gstreamer1.0-plugins-ugly \
    gstreamer1.0-plugins-good gstreamer1.0-plugins-base gstreamer1.0-libav \
 && update-ca-certificates \
 && rm -rf /var/lib/apt/lists/*



# build
WORKDIR /opt
RUN git clone --depth 1 https://github.com/meetecho/simple-whip-client.git && \
    cd simple-whip-client && make && \
    cp whip-client /usr/local/bin && \
    cd .. && rm -rf simple-whip-client

ENV URL=http://node-server:7070/whip/abc123 \
    TOKEN= \
    AUDIO_PIPE="audiotestsrc is-live=true wave=red-noise ! audioconvert ! audioresample ! queue ! opusenc ! rtpopuspay pt=100 ssrc=1 ! queue ! application/x-rtp,media=audio,encoding-name=OPUS,payload=100" \
    VIDEO_PIPE="videotestsrc is-live=true pattern=ball ! videoconvert ! queue ! vp8enc deadline=1 ! rtpvp8pay pt=96 ssrc=2 ! queue ! application/x-rtp,media=video,encoding-name=VP8,payload=96" \
    STUN_SERVER=stun://stun.l.google.com:19302 \
    GST_DEBUG=2

ENTRYPOINT ["/bin/sh","-c","exec whip-client -u \"$URL\" ${TOKEN:+-t $TOKEN} -A \"$AUDIO_PIPE\" -V \"$VIDEO_PIPE\" -S \"$STUN_SERVER\""]
