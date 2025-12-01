#include <gst/gst.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h> // socket
#include <sys/un.h>     // socket unix
#include <unistd.h>

#include <cjson/cJSON.h>
#include <pthread.h>

#define BUFFER_SIZE 512
#define MAX_SESSIONS 200
#define MAX_TARGETS_PER_SESSION 50

pthread_mutex_t sessions_mutex = PTHREAD_MUTEX_INITIALIZER;

// Singola destinazione (egress o relay) per una session
typedef struct {
    char targetId[64]; // es: "egress-1"
    char host[256];    // es: "egress-1"
    int audioPort;     // es: 5002
    int videoPort;     // es: 5004

    // GStreamer elements
    GstElement *audioQueue;
    GstElement *audioSink;
    GstElement *videoQueue;
    GstElement *videoSink;

    // Pad dal tee (per cleanup)
    GstPad *audioTeeSrcPad;
    GstPad *videoTeeSrcPad;

} TargetRoute;

// Session con tutte le sue route
typedef struct {
    char sessionId[64]; // es: "broadcaster-1"
    int audioSsrc;      // es: 1111
    int videoSsrc;      // es: 2222

    // GStreamer tee
    GstElement *audioTee;
    GstElement *videoTee;
    GstPad *audioDemuxPad; // Pad da rtpssrcdemux
    GstPad *videoDemuxPad;

    // destinazioni
    TargetRoute targets[MAX_TARGETS_PER_SESSION];
    int targetCount;

} SessionRoute;

int server_socket = -1;     // socket file descriptor    ACCETTARE connessioni
int client_connection = -1; // client file descriptor (nodejs)  COMUNICARE con il client
char socket_path[256];      // (es: /tmp/relay-forwarder-relay-1.sock)

// flag cleanup
static int cleaning = 0;

// GStreamer
GstElement *audio_pipeline = NULL;
GstElement *video_pipeline = NULL;
GstElement *audio_demux = NULL;
GstElement *video_demux = NULL;

// Session tracking
SessionRoute sessions[MAX_SESSIONS];
int session_count = 0;

// HELPER FUNCTIONS

SessionRoute *find_session_by_id(const char *sessionId) {
    for (int i = 0; i < session_count; i++) {
        if (strcmp(sessions[i].sessionId, sessionId) == 0) {
            return &sessions[i];
        }
    }
    return NULL;
}

SessionRoute *find_session_by_audio_ssrc(int ssrc) {
    for (int i = 0; i < session_count; i++) {
        if (sessions[i].audioSsrc == ssrc) {
            return &sessions[i];
        }
    }
    return NULL;
}

SessionRoute *find_session_by_video_ssrc(int ssrc) {
    for (int i = 0; i < session_count; i++) {
        if (sessions[i].videoSsrc == ssrc) {
            return &sessions[i];
        }
    }
    return NULL;
}

TargetRoute *find_target(SessionRoute *session, const char *targetId) {
    for (int i = 0; i < session->targetCount; i++) {
        if (strcmp(session->targets[i].targetId, targetId) == 0) {
            return &session->targets[i];
        }
    }
    return NULL;
}

void cleanup_and_exit(int sig) {
    (void)sig; // evita warning

    if (cleaning) {
        return;
    }
    cleaning = 1;

    printf(" Cleaning up\n");

    // Stop GStreamer
    if (audio_pipeline) {
        gst_element_set_state(audio_pipeline, GST_STATE_NULL);
        gst_object_unref(audio_pipeline);
    }
    if (video_pipeline) {
        gst_element_set_state(video_pipeline, GST_STATE_NULL);
        gst_object_unref(video_pipeline);
    }

    // chiudi socket
    if (client_connection >= 0)
        close(client_connection);

    if (server_socket >= 0)
        close(server_socket);

    // rimuovi socket da filesystem
    if (strlen(socket_path) > 0) {
        unlink(socket_path);
        printf("Socket removed: %s\n", socket_path);
    }

    exit(0);
}

// TEE MANAGEMENT

// Crea tee e collega pad esistente dal demux
GstElement *create_tee_and_link_pad(GstPad *demuxPad, gboolean isAudio) {
    GstElement *pipeline = isAudio ? audio_pipeline : video_pipeline;

    // Crea tee
    GstElement *tee = gst_element_factory_make("tee", NULL);
    if (!tee) {
        fprintf(stderr, "Failed to create tee\n");
        return NULL;
    }

    // Config tee
    // permette di non avere destinazioni nel tee
    g_object_set(tee, "allow-not-linked", TRUE, NULL);

    // Aggiungi alla pipeline
    gst_bin_add(GST_BIN(pipeline), tee);

    // Collega demux pad -> tee sink pad
    GstPad *teeSinkPad = gst_element_get_static_pad(tee, "sink");
    GstPadLinkReturn linkRet = gst_pad_link(demuxPad, teeSinkPad);
    gst_object_unref(teeSinkPad);

    if (linkRet != GST_PAD_LINK_OK) {
        fprintf(stderr, "Failed to link demux to tee\n");
        gst_bin_remove(GST_BIN(pipeline), tee);
        gst_object_unref(tee);
        return NULL;
    }

    // Sincronizza stato
    gst_element_sync_state_with_parent(tee);
    gst_element_set_state(tee, GST_STATE_PLAYING);

    return tee;
}

// Collega un target al tee (crea queue + udpsink)
int link_target_to_tee(SessionRoute *session, TargetRoute *target) {
    gboolean linked_audio = FALSE;
    gboolean linked_video = FALSE;

    // AUDIO
    if (session->audioTee) {
        if (target->audioQueue != NULL) {
            linked_audio = TRUE;
        } else {
            target->audioQueue = gst_element_factory_make("queue", NULL);
            target->audioSink = gst_element_factory_make("udpsink", NULL);

            if (!target->audioQueue || !target->audioSink) {
                fprintf(stderr, "Failed to create audio queue/sink\n");
                return -1;
            }
            // Config
            g_object_set(target->audioQueue,
                         "max-size-buffers", 20,
                         "max-size-bytes", 0,
                         "max-size-time", 0,
                         NULL);

            g_object_set(target->audioSink,
                         "host", target->host,
                         "port", target->audioPort,
                         "sync", FALSE,
                         "async", FALSE,
                         NULL);

            // Add to pipeline
            gst_bin_add_many(GST_BIN(audio_pipeline),
                             target->audioQueue,
                             target->audioSink,
                             NULL);

            // Link queue -> sink
            if (!gst_element_link(target->audioQueue, target->audioSink)) {
                fprintf(stderr, "Failed to link audio queue to sink\n");
                return -1;
            }

            // Request src pad from tee
            GstPad *teeSrcPad = gst_element_request_pad_simple(session->audioTee, "src_%u");
            if (!teeSrcPad) {
                fprintf(stderr, "Failed to request pad from audio tee\n");
                return -1;
            }

            // Link tee -> queue
            GstPad *queueSinkPad = gst_element_get_static_pad(target->audioQueue, "sink");
            GstPadLinkReturn linkRet = gst_pad_link(teeSrcPad, queueSinkPad);
            gst_object_unref(queueSinkPad);

            if (linkRet != GST_PAD_LINK_OK) {
                fprintf(stderr, "Failed to link tee to queue\n");
                gst_element_release_request_pad(session->audioTee, teeSrcPad);
                gst_object_unref(teeSrcPad);
                return -1;
            }

            target->audioTeeSrcPad = teeSrcPad;

            // Sync state
            gst_element_sync_state_with_parent(target->audioQueue);
            gst_element_sync_state_with_parent(target->audioSink);

            // pad dangling recovery richiede playing esplicito degli elementi
            gst_element_set_state(target->audioQueue, GST_STATE_PLAYING);
            gst_element_set_state(target->audioSink, GST_STATE_PLAYING);

            linked_audio = TRUE;
        }
    }

    // VIDEO
    if (session->videoTee) {
        if (target->videoQueue != NULL) {
            linked_video = TRUE;
        } else {
            target->videoQueue = gst_element_factory_make("queue", NULL);
            target->videoSink = gst_element_factory_make("udpsink", NULL);

            if (!target->videoQueue || !target->videoSink) {
                fprintf(stderr, "Failed to create video queue/sink\n");
                return -1;
            }

            // Config
            g_object_set(target->videoQueue,
                         "max-size-buffers", 10,
                         "max-size-bytes", 0,
                         "max-size-time", 0,
                         NULL);

            g_object_set(target->videoSink,
                         "host", target->host,
                         "port", target->videoPort,
                         "sync", FALSE,
                         "async", FALSE,
                         NULL);

            // Add to pipeline
            gst_bin_add_many(GST_BIN(video_pipeline),
                             target->videoQueue,
                             target->videoSink,
                             NULL);

            // Link queue -> sink
            if (!gst_element_link(target->videoQueue, target->videoSink)) {
                fprintf(stderr, "Failed to link video queue to sink\n");
                return -1;
            }

            // Request src pad from tee
            GstPad *teeSrcPad = gst_element_request_pad_simple(session->videoTee, "src_%u");
            if (!teeSrcPad) {
                fprintf(stderr, "Failed to request pad from video tee\n");
                return -1;
            }

            // Link tee -> queue
            GstPad *queueSinkPad = gst_element_get_static_pad(target->videoQueue, "sink");
            GstPadLinkReturn linkRet = gst_pad_link(teeSrcPad, queueSinkPad);
            gst_object_unref(queueSinkPad);

            if (linkRet != GST_PAD_LINK_OK) {
                fprintf(stderr, "Failed to link tee to queue\n");
                gst_element_release_request_pad(session->videoTee, teeSrcPad);
                gst_object_unref(teeSrcPad);
                return -1;
            }

            target->videoTeeSrcPad = teeSrcPad;

            // Sync state
            gst_element_sync_state_with_parent(target->videoQueue);
            gst_element_sync_state_with_parent(target->videoSink);

            // pad dangling recovery richiede playing esplicito degli elementi
            gst_element_set_state(target->videoQueue, GST_STATE_PLAYING);
            gst_element_set_state(target->videoSink, GST_STATE_PLAYING);
            linked_video = TRUE;
        }
    }
    if (!linked_audio && !linked_video) {
        printf("Target saved but not linked\n");
    }

    return 0;
}

// Scollega target dal tee
void unlink_target_from_tee(SessionRoute *session, TargetRoute *target) {
    // Audio cleanup
    if (target->audioQueue && target->audioSink) {
        if (target->audioTeeSrcPad) {
            GstPad *queueSinkPad = gst_element_get_static_pad(target->audioQueue, "sink");
            if (queueSinkPad) {
                gst_pad_unlink(target->audioTeeSrcPad, queueSinkPad);
                gst_object_unref(queueSinkPad);
            }
            gst_element_release_request_pad(session->audioTee, target->audioTeeSrcPad);
            gst_object_unref(target->audioTeeSrcPad);
            target->audioTeeSrcPad = NULL;
        }

        gst_element_set_state(target->audioQueue, GST_STATE_NULL);
        gst_element_set_state(target->audioSink, GST_STATE_NULL);
        gst_bin_remove_many(GST_BIN(audio_pipeline),
                            target->audioQueue,
                            target->audioSink,
                            NULL);
    }

    // Video cleanup
    if (target->videoQueue && target->videoSink) {
        if (target->videoTeeSrcPad) {
            GstPad *queueSinkPad = gst_element_get_static_pad(target->videoQueue, "sink");
            if (queueSinkPad) {
                gst_pad_unlink(target->videoTeeSrcPad, queueSinkPad);
                gst_object_unref(queueSinkPad);
            }
            gst_element_release_request_pad(session->videoTee, target->videoTeeSrcPad);
            gst_object_unref(target->videoTeeSrcPad);
            target->videoTeeSrcPad = NULL;
        }

        gst_element_set_state(target->videoQueue, GST_STATE_NULL);
        gst_element_set_state(target->videoSink, GST_STATE_NULL);
        gst_bin_remove_many(GST_BIN(video_pipeline),
                            target->videoQueue,
                            target->videoSink,
                            NULL);
    }
}

// DYNAMIC PAD CALLBACKS

void on_audio_pad_added(GstElement *demux, guint ssrc, GstPad *pad, gpointer user_data) {
    (void)demux;
    (void)user_data;

    printf("New audio SSRC detected: %u\n", ssrc);

    pthread_mutex_lock(&sessions_mutex);

    // Cerca session per questo SSRC
    SessionRoute *session = find_session_by_audio_ssrc((int)ssrc);

    if (!session) {
        printf(" Audio SSRC %u not registered - attaching temporary FAKESINK\n", ssrc);

        // Collega temporaneamente a fakesink per evitare NOT_LINKED
        GstElement *fakesink = gst_element_factory_make("fakesink", NULL);
        g_object_set(fakesink,
                     "sync", FALSE,
                     "async", FALSE,
                     NULL);

        gst_bin_add(GST_BIN(audio_pipeline), fakesink);

        GstPad *sinkPad = gst_element_get_static_pad(fakesink, "sink");
        // collega il pad di rtpssrcdemux al sink
        gst_pad_link(pad, sinkPad);
        gst_object_unref(sinkPad);

        gst_element_sync_state_with_parent(fakesink);
        gst_element_set_state(fakesink, GST_STATE_PLAYING);

        // Salva fakesink nel pad per rimuoverlo dopo
        g_object_set_data(G_OBJECT(pad), "dangling-fakesink", fakesink);

        pthread_mutex_unlock(&sessions_mutex);
        return;
    }

    printf("Found session %s for audio SSRC %u\n", session->sessionId, ssrc);

    // Crea tee e collega pad
    // in teoria se arriva qui entra sempre nell'if
    if (!session->audioTee) {
        session->audioDemuxPad = pad;
        session->audioTee = create_tee_and_link_pad(pad, TRUE);

        if (session->audioTee) {
            printf("Audio tee created and linked\n");

            // Collega tutti i target già configurati
            for (int i = 0; i < session->targetCount; i++) {
                printf("Linking target %s\n", session->targets[i].targetId);
                link_target_to_tee(session, &session->targets[i]);
            }
        }
    }

    pthread_mutex_unlock(&sessions_mutex);
}

void on_video_pad_added(GstElement *demux, guint ssrc, GstPad *pad, gpointer user_data) {
    (void)demux;
    (void)user_data;

    printf("New video SSRC detected: %u\n", ssrc);

    pthread_mutex_lock(&sessions_mutex);

    // Cerca session per questo SSRC
    SessionRoute *session = find_session_by_video_ssrc((int)ssrc);

    if (!session) {
        printf(" Video SSRC %u not registered - attaching temporary FAKESINK\n", ssrc);
        // Collega temporaneamente a fakesink per evitare NOT_LINKED
        GstElement *fakesink = gst_element_factory_make("fakesink", NULL);
        g_object_set(fakesink,
                     "sync", FALSE,
                     "async", FALSE,
                     NULL);

        gst_bin_add(GST_BIN(video_pipeline), fakesink);

        GstPad *sinkPad = gst_element_get_static_pad(fakesink, "sink");
        gst_pad_link(pad, sinkPad);
        gst_object_unref(sinkPad);

        gst_element_sync_state_with_parent(fakesink);
        gst_element_set_state(fakesink, GST_STATE_PLAYING);

        // Salva fakesink nel pad per rimuoverlo dopo
        g_object_set_data(G_OBJECT(pad), "dangling-fakesink", fakesink);

        pthread_mutex_unlock(&sessions_mutex);
        return;
    }

    printf("Found session %s for video SSRC %u\n", session->sessionId, ssrc);

    // Crea tee e collega pad
    if (!session->videoTee) {
        session->videoDemuxPad = pad;
        session->videoTee = create_tee_and_link_pad(pad, FALSE);

        if (session->videoTee) {
            printf("Video tee created and linked\n");

            // Collega tutti i target già configurati
            for (int i = 0; i < session->targetCount; i++) {
                printf("Linking target %s\n", session->targets[i].targetId);
                link_target_to_tee(session, &session->targets[i]);
            }
        }
    }

    pthread_mutex_unlock(&sessions_mutex);
}

// GSTREAMER SETUP

// Crea e avvia due pipeline: audio e video
int setup_pipelines(int audio_port, int video_port) {
    printf("Setting up GStreamer pipelines\n");

    // Audio pipeline: udpsrc -> rtpssrcdemux -> tee
    audio_pipeline = gst_pipeline_new("audio-pipeline");
    GstElement *audio_src = gst_element_factory_make("udpsrc", "audio-src");
    audio_demux = gst_element_factory_make("rtpssrcdemux", "audio-demux");

    if (!audio_pipeline || !audio_src || !audio_demux) {
        fprintf(stderr, "Failed to create audio pipeline elements\n");
        return -1;
    }

    // configura porta
    g_object_set(audio_src,
                 "port", audio_port,
                 "caps", gst_caps_from_string("application/x-rtp"),
                 NULL);

    g_signal_connect(audio_demux, "new-ssrc-pad",
                     G_CALLBACK(on_audio_pad_added), NULL);

    // Build audio pipeline
    gst_bin_add_many(GST_BIN(audio_pipeline), audio_src, audio_demux, NULL);

    // link logico tra i due elementi
    if (!gst_element_link(audio_src, audio_demux)) {
        fprintf(stderr, "Failed to link audio src to demux\n");
        return -1;
    }

    gst_element_set_state(audio_pipeline, GST_STATE_PLAYING);
    printf(" Audio pipeline ready (port %d)\n", audio_port);

    // Video pipeline: udpsrc -> rtpssrcdemux -> tee
    video_pipeline = gst_pipeline_new("video-pipeline");
    GstElement *video_src = gst_element_factory_make("udpsrc", "video-src");
    video_demux = gst_element_factory_make("rtpssrcdemux", "video-demux");

    if (!video_pipeline || !video_src || !video_demux) {
        fprintf(stderr, "Failed to create video pipeline elements\n");
        return -1;
    }

    // configura porta
    g_object_set(video_src,
                 "port", video_port,
                 "caps", gst_caps_from_string("application/x-rtp"),
                 NULL);

    g_signal_connect(video_demux, "new-ssrc-pad",
                     G_CALLBACK(on_video_pad_added), NULL);

    // Build video pipeline
    gst_bin_add_many(GST_BIN(video_pipeline), video_src, video_demux, NULL);

    // link logico tra i due elementi (unidirezionale)
    if (!gst_element_link(video_src, video_demux)) {
        fprintf(stderr, "Failed to link video src to demux\n");
        return -1;
    }

    gst_element_set_state(video_pipeline, GST_STATE_PLAYING);
    printf(" Video pipeline ready (port %d)\n", video_port);

    return 0;
}

// COMMAND HANDLERS

// Gestisce comando: ADD
void handle_add_session(const char *sessionId, int audioSsrc, int videoSsrc) {
    printf("ADD_SESSION: %s (audio=%d, video=%d)\n", sessionId, audioSsrc, videoSsrc);

    pthread_mutex_lock(&sessions_mutex);

    // Check duplicato
    if (find_session_by_id(sessionId)) {
        fprintf(stderr, "Session %s already exists\n", sessionId);
        write(client_connection, "ERROR: Session exists\n", 22);
        pthread_mutex_unlock(&sessions_mutex);
        return;
    }

    // Check limite
    if (session_count >= MAX_SESSIONS) {
        fprintf(stderr, "Max sessions reached\n");
        write(client_connection, "ERROR: Max sessions\n", 20);
        pthread_mutex_unlock(&sessions_mutex);
        return;
    }

    // Crea session
    SessionRoute *session = &sessions[session_count++];
    memset(session, 0, sizeof(SessionRoute));
    strncpy(session->sessionId, sessionId, sizeof(session->sessionId) - 1);
    session->audioSsrc = audioSsrc;
    session->videoSsrc = videoSsrc;

    // PAD DANGLING RECOVERY: Controlla se pad esiste già
    gchar audioPadName[64];
    gchar videoPadName[64];
    snprintf(audioPadName, sizeof(audioPadName), "src_%u", audioSsrc);
    snprintf(videoPadName, sizeof(videoPadName), "src_%u", videoSsrc);
    GstPad *existingAudioPad = gst_element_get_static_pad(audio_demux, audioPadName);
    GstPad *existingVideoPad = gst_element_get_static_pad(video_demux, videoPadName);

    if (existingAudioPad) {
        printf("Found existing DANGLING audio pad - recovering\n");

        // Rimuovi fakesink se presente
        GstElement *fakesink = g_object_get_data(G_OBJECT(existingAudioPad), "dangling-fakesink");
        if (fakesink) {
            GstPad *sinkPad = gst_element_get_static_pad(fakesink, "sink");
            gst_pad_unlink(existingAudioPad, sinkPad);
            gst_object_unref(sinkPad);

            gst_element_set_state(fakesink, GST_STATE_NULL);
            gst_bin_remove(GST_BIN(audio_pipeline), fakesink);
            g_object_set_data(G_OBJECT(existingAudioPad), "dangling-fakesink", NULL);
            gst_object_unref(existingAudioPad);
        }

        session->audioDemuxPad = existingAudioPad;
        session->audioTee = create_tee_and_link_pad(existingAudioPad, TRUE);
    }

    if (existingVideoPad) {
        printf("Found existing DANGLING video pad - recovering\n");

        // Rimuovi fakesink se presente
        GstElement *fakesink = g_object_get_data(G_OBJECT(existingVideoPad), "dangling-fakesink");
        if (fakesink) {
            GstPad *sinkPad = gst_element_get_static_pad(fakesink, "sink");
            gst_pad_unlink(existingVideoPad, sinkPad);
            gst_object_unref(sinkPad);

            gst_element_set_state(fakesink, GST_STATE_NULL);
            gst_bin_remove(GST_BIN(video_pipeline), fakesink);
            g_object_set_data(G_OBJECT(existingVideoPad), "dangling-fakesink", NULL);
            gst_object_unref(existingVideoPad);
        }

        session->videoDemuxPad = existingVideoPad;
        session->videoTee = create_tee_and_link_pad(existingVideoPad, FALSE);
    }

    pthread_mutex_unlock(&sessions_mutex);

    write(client_connection, "OK\n", 3);
    printf("Session %s added\n", sessionId);
}

void handle_add_route(const char *sessionId, const char *targetId,
                      const char *host, int audioPort, int videoPort) {
    printf("ADD_ROUTE: %s -> %s (%s:%d/%d)\n",
           sessionId, targetId, host, audioPort, videoPort);

    pthread_mutex_lock(&sessions_mutex);

    // Trova session
    SessionRoute *session = find_session_by_id(sessionId);
    if (!session) {
        fprintf(stderr, "Session %s not found\n", sessionId);
        write(client_connection, "ERROR: Session not found\n", 25);
        pthread_mutex_unlock(&sessions_mutex);
        return;
    }

    // Check duplicato
    if (find_target(session, targetId)) {
        fprintf(stderr, "Route %s already exists\n", targetId);
        write(client_connection, "OK\n", 3);
        pthread_mutex_unlock(&sessions_mutex);
        return;
    }

    // Check limite
    if (session->targetCount >= MAX_TARGETS_PER_SESSION) {
        fprintf(stderr, "Max targets reached for session\n");
        write(client_connection, "ERROR: Max targets\n", 19);
        pthread_mutex_unlock(&sessions_mutex);
        return;
    }

    // Crea target
    TargetRoute *target = &session->targets[session->targetCount++];
    memset(target, 0, sizeof(TargetRoute));
    strncpy(target->targetId, targetId, sizeof(target->targetId) - 1);
    strncpy(target->host, host, sizeof(target->host) - 1);
    target->audioPort = audioPort;
    target->videoPort = videoPort;

    // Collega al tee (se tee esiste)
    int result = link_target_to_tee(session, target);

    gboolean tee_exists = (session->audioTee != NULL || session->videoTee != NULL);

    pthread_mutex_unlock(&sessions_mutex);

    if (result == 0) {
        write(client_connection, "OK\n", 3);
        if (tee_exists) {
            printf("Route %s added and linked\n", targetId);
        } else {
            printf("Route %s added (pending RTP)\n", targetId);
        }
    } else {
        write(client_connection, "ERROR: Failed to link\n", 22);
    }
}

void handle_remove_route(const char *sessionId, const char *targetId) {
    printf("REMOVE_ROUTE: %s -> %s\n", sessionId, targetId);

    pthread_mutex_lock(&sessions_mutex);

    // Trova session
    SessionRoute *session = find_session_by_id(sessionId);
    if (!session) {
        fprintf(stderr, "Session %s not found\n", sessionId);
        write(client_connection, "ERROR: Session not found\n", 25);
        pthread_mutex_unlock(&sessions_mutex);
        return;
    }

    // Trova target e il suo indice
    int target_index = -1;
    TargetRoute *target = NULL;

    for (int i = 0; i < session->targetCount; i++) {
        if (strcmp(session->targets[i].targetId, targetId) == 0) {
            target = &session->targets[i];
            target_index = i;
            break;
        }
    }

    if (!target) {
        fprintf(stderr, "Target %s not found\n", targetId);
        write(client_connection, "ERROR: Target not found\n", 24);
        pthread_mutex_unlock(&sessions_mutex);
        return;
    }

    // Scollega
    unlink_target_from_tee(session, target);

    // Compatta array target
    if (target_index != session->targetCount - 1)
        session->targets[target_index] = session->targets[session->targetCount - 1];

    session->targetCount--;

    pthread_mutex_unlock(&sessions_mutex);

    write(client_connection, "OK\n", 3);
    printf("Route %s removed\n", targetId);
}

void handle_remove_session(const char *sessionId) {
    printf("REMOVE_SESSION: %s\n", sessionId);

    pthread_mutex_lock(&sessions_mutex);

    // Trova session e il suo indice
    int session_index = -1;
    SessionRoute *session = NULL;

    for (int i = 0; i < session_count; i++) {
        if (strcmp(sessions[i].sessionId, sessionId) == 0) {
            session = &sessions[i];
            session_index = i;
            break;
        }
    }

    if (!session) {
        fprintf(stderr, "Session %s not found\n", sessionId);
        write(client_connection, "ERROR: Session not found\n", 25);
        pthread_mutex_unlock(&sessions_mutex);
        return;
    }

    // Rimuovi tutti i target
    for (int i = 0; i < session->targetCount; i++) {
        unlink_target_from_tee(session, &session->targets[i]);
    }

    // Rimuovi tee
    if (session->audioTee) {
        gst_element_set_state(session->audioTee, GST_STATE_NULL);
        gst_bin_remove(GST_BIN(audio_pipeline), session->audioTee);
    }
    if (session->videoTee) {
        gst_element_set_state(session->videoTee, GST_STATE_NULL);
        gst_bin_remove(GST_BIN(video_pipeline), session->videoTee);
    }

    // Pulisci SSRC dal demux
    if (audio_demux && session->audioDemuxPad) {
        printf("Clearing audio SSRC %d from demux\n", session->audioSsrc);
        g_signal_emit_by_name(audio_demux, "clear-ssrc", (guint)session->audioSsrc);
    }
    if (video_demux && session->videoDemuxPad) {
        printf(" Clearing video SSRC %d from demux\n", session->videoSsrc);
        g_signal_emit_by_name(video_demux, "clear-ssrc", (guint)session->videoSsrc);
    }

    // Compatta array
    if (session_index != session_count - 1)
        sessions[session_index] = sessions[session_count - 1];

    session_count--;

    pthread_mutex_unlock(&sessions_mutex);

    write(client_connection, "OK\n", 3);
    printf("Session %s removed (active sessions: %d)\n", sessionId, session_count);
}

void handle_list() {
    pthread_mutex_lock(&sessions_mutex);

    cJSON *root = cJSON_CreateObject();
    cJSON *sessions_array = cJSON_CreateArray();

    for (int i = 0; i < session_count; i++) {
        cJSON *session_obj = cJSON_CreateObject();
        cJSON_AddStringToObject(session_obj, "sessionId", sessions[i].sessionId);
        cJSON_AddNumberToObject(session_obj, "audioSsrc", sessions[i].audioSsrc);
        cJSON_AddNumberToObject(session_obj, "videoSsrc", sessions[i].videoSsrc);
        cJSON_AddNumberToObject(session_obj, "targetCount", sessions[i].targetCount);
        cJSON_AddBoolToObject(session_obj, "audioTeeReady", sessions[i].audioTee != NULL);
        cJSON_AddBoolToObject(session_obj, "videoTeeReady", sessions[i].videoTee != NULL);

        // Targets
        cJSON *targets_array = cJSON_CreateArray();
        for (int j = 0; j < sessions[i].targetCount; j++) {
            cJSON *target_obj = cJSON_CreateObject();
            cJSON_AddStringToObject(target_obj, "targetId", sessions[i].targets[j].targetId);
            cJSON_AddStringToObject(target_obj, "host", sessions[i].targets[j].host);
            cJSON_AddNumberToObject(target_obj, "audioPort", sessions[i].targets[j].audioPort);
            cJSON_AddNumberToObject(target_obj, "videoPort", sessions[i].targets[j].videoPort);
            cJSON_AddItemToArray(targets_array, target_obj);
        }
        cJSON_AddItemToObject(session_obj, "targets", targets_array);

        cJSON_AddItemToArray(sessions_array, session_obj);
    }

    cJSON_AddItemToObject(root, "sessions", sessions_array);

    char *json_string = cJSON_PrintUnformatted(root);
    if (json_string) {
        write(client_connection, json_string, strlen(json_string));
        write(client_connection, "\n", 1);
        write(client_connection, "END\n", 4);
        free(json_string);
    }

    cJSON_Delete(root);

    pthread_mutex_unlock(&sessions_mutex);
}

// SOCKET COMMAND LOOP

void *command_loop(void) {
    char buffer[BUFFER_SIZE];
    char c; // carattere corrente

    printf(" Command loop ready\n");

    while (1) {
        int i = 0;
        ssize_t n; // signed size

        // Leggi carattere per carattere fino a '\n'
        while (i < BUFFER_SIZE - 1) {
            n = read(client_connection, &c, 1); // read(int fd, void *buf, size_t count);

            // disconnessioni/errori
            if (n <= 0) {
                printf(" Client disconnected\n");
                // Chiudi socket corrente
                close(client_connection);

                printf(" Waiting for client reconnection...\n");

                // Aspetta nuova connessione (bloccante)
                client_connection = accept(server_socket, NULL, NULL);

                if (client_connection < 0) {
                    perror(" accept() failed during reconnection");
                    cleanup_and_exit(1);
                }

                printf(" Client reconnected!\n");

                // ricomincia a leggere
                i = 0;
                continue;
            }
            if (c == '\n')
                break;
            buffer[i++] = c;
        }

        buffer[i] = '\0'; // terminatore

        // ingnora linea vuota
        if (strlen(buffer) == 0)
            continue;

        printf(" Received: '%s'\n", buffer);

        // GESTIONE COMANDO

        // Parse comando
        /*
         * Comandi supportati:
         *
         * ADD_SESSION <sessionId> <audioSsrc> <videoSsrc>
         *   Es: ADD_SESSION broadcaster-1 1111 2222
         *
         * ADD_ROUTE <sessionId> <targetId> <host> <audioPort> <videoPort>
         *   Es: ADD_ROUTE broadcaster-1 egress-1 egress-1 5002 5004
         *
         * REMOVE_ROUTE <sessionId> <targetId>
         *   Es: REMOVE_ROUTE broadcaster-1 egress-1
         *
         * REMOVE_SESSION <sessionId>
         *   Es: REMOVE_SESSION broadcaster-1
         *
         * LIST
         *   Ritorna JSON con tutte le sessioni e route
         *
         * PING
         *   Risponde PONG
         *
         * SHUTDOWN
         *   Chiude il forwarder
         */
        char cmd[64], arg1[256], arg2[256], arg3[256], arg4[16], arg5[16];
        int parsed = sscanf(buffer, "%63s %255s %255s %255s %15s %15s",
                            cmd, arg1, arg2, arg3, arg4, arg5);

        if (strcmp(cmd, "ADD_SESSION") == 0 && parsed >= 4) {
            handle_add_session(arg1, atoi(arg2), atoi(arg3));

        } else if (strcmp(cmd, "ADD_ROUTE") == 0 && parsed >= 6) {
            handle_add_route(arg1, arg2, arg3, atoi(arg4), atoi(arg5));

        } else if (strcmp(cmd, "REMOVE_ROUTE") == 0 && parsed >= 3) {
            handle_remove_route(arg1, arg2);

        } else if (strcmp(cmd, "REMOVE_SESSION") == 0 && parsed >= 2) {
            handle_remove_session(arg1);

        } else if (strcmp(cmd, "LIST") == 0) {
            handle_list();

        } else if (strcmp(cmd, "PING") == 0) {
            write(client_connection, "PONG\n", 5);

        } else if (strcmp(cmd, "SHUTDOWN") == 0) {
            write(client_connection, "BYE\n", 4);
            cleanup_and_exit(0);
            return NULL;
        } else {
            fprintf(stderr, "Unknown command: %s\n", buffer);
            write(client_connection, "ERROR: Unknown command\n", 23);
        }
    }
}

// MAIN

int main(int argc, char *argv[]) {
    if (argc < 4) {
        fprintf(stderr, "Usage: %s <nodeId> <audioPort> <videoPort>\n", argv[0]);
        return 1;
    }

    const char *node_id = argv[1];
    int audio_port = atoi(argv[2]);
    int video_port = atoi(argv[3]);

    // disabilita buffering altrimenti non stampa log
    setbuf(stdout, NULL);
    setbuf(stderr, NULL);

    // signal handler shutdown
    signal(SIGTERM, cleanup_and_exit);
    signal(SIGINT, cleanup_and_exit);

    // Costruisci path del socket
    snprintf(socket_path, sizeof(socket_path),
             "/tmp/relay-forwarder-%s.sock", node_id);

    printf(" Starting relay-forwarder for node: %s\n", node_id);
    printf(" Socket path: %s\n", socket_path);
    printf(" Audio port: %d\n", audio_port);
    printf(" Video port: %d\n", video_port);

    // INIZIALIZZA GSTREAMER
    gst_init(&argc, &argv);

    // crea e avvia pipelines
    if (setup_pipelines(audio_port, video_port) < 0) {
        return 1;
    }

    // Rimuovi socket esistente (se c'è)
    unlink(socket_path);

    // crea socket unix di tipo stream (0 = protocollo predefinito)
    server_socket = socket(AF_UNIX, SOCK_STREAM, 0);
    if (server_socket < 0) {
        perror(" socket() failed");
        return 1;
    }

    // bind al path
    // socket addresses
    struct sockaddr_un addr;
    // cleanup struttura (azzerata)
    memset(&addr, 0, sizeof(addr));
    addr.sun_family = AF_UNIX; // unix address family
    strncpy(addr.sun_path, socket_path, sizeof(addr.sun_path) - 1);

    // Associa socket al path
    if (bind(server_socket, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
        perror(" bind() failed");
        close(server_socket);
        return 1;
    }

    // socket in ascolto
    if (listen(server_socket, 1) < 0) {
        perror(" listen() failed");
        cleanup_and_exit(0);
        return 1;
    }

    printf(" Waiting for connection\n");

    // Accept (blocca finché Node.js non si connette)
    client_connection = accept(server_socket, NULL, NULL);
    if (client_connection < 0) {
        perror(" accept() failed");
        cleanup_and_exit(0);
        return 1;
    }

    printf(" Client connected!\n");

    // loop infinito
    command_loop();

    return 0;
}