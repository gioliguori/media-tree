#include <gst/gst.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/un.h>
#include <unistd.h>

#include <cjson/cJSON.h>
#include <pthread.h>

#define BUFFER_SIZE 256
#define MAX_MOUNTPOINTS 100

pthread_mutex_t audio_mutex = PTHREAD_MUTEX_INITIALIZER;
pthread_mutex_t video_mutex = PTHREAD_MUTEX_INITIALIZER;

// STRUTTURE DATI

typedef struct {
    char sessionId[64];  // broadcaster
    int ssrc;            // audio o video
    int destinationPort; // Janus

    // Elementi GStreamer (NULL finché non arriva RTP)
    GstElement *queue;
    GstElement *udpsink;
    GstPad *demuxPad; // creato da GStreamer

    int active; // 1 = attivo, 0 = inattivo
} SsrcMapping;

// VARIABILI GLOBALI

// Socket Unix
int server_socket = -1;
int client_connection = -1;
char socket_path[256];

// Cleanup flag
static int cleanup_in_progress = 0;

// GStreamer pipelines
GstElement *audio_pipeline = NULL;
GstElement *video_pipeline = NULL;
GstElement *audio_demux = NULL;
GstElement *video_demux = NULL;

// SSRC mappings
SsrcMapping audio_mappings[MAX_MOUNTPOINTS];
SsrcMapping video_mappings[MAX_MOUNTPOINTS];
int audio_count = 0;
int video_count = 0;

// Destination host
char destination_host[256];

// CLEANUP

void cleanup_and_exit(int sig) {
    (void)sig; // evita warning

    // previene doppio cleanup
    if (cleanup_in_progress) {
        return;
    }
    cleanup_in_progress = 1;

    printf(" Cleaning up...\n");

    // Stop GStreamer pipelines
    if (audio_pipeline) {
        gst_element_set_state(audio_pipeline, GST_STATE_NULL);
        gst_object_unref(audio_pipeline);
    }
    if (video_pipeline) {
        gst_element_set_state(video_pipeline, GST_STATE_NULL);
        gst_object_unref(video_pipeline);
    }

    // chiudi socket
    if (client_connection >= 0) {
        close(client_connection);
    }
    if (server_socket >= 0) {
        close(server_socket);
    }

    // rimuovi socket da filesystem
    if (strlen(socket_path) > 0) {
        unlink(socket_path);
        printf(" Socket removed: %s\n", socket_path);
    }

    exit(0);
}

// DYNAMIC PAD CALLBACK
// GStreamer chiama questa funzione automaticamente quando rtpssrcdemux rileva un nuovo SSRC
// quindi rtpssrcdemux crea i pad noi dobbiamo solo collegarli con questa callback??!?
void on_audio_pad_added(GstElement *demux, guint ssrc, GstPad *pad, gpointer user_data) {
    // guint = GLib Unsigned Integer
    // gpointer = GLib Generic Pointer
    // non li usiamo ma la firma della callback è questa
    (void)demux;
    (void)user_data;

    printf(" New audio SSRC detected: %u\n", ssrc);

    // lock
    pthread_mutex_lock(&audio_mutex);

    // Cerca mapping per questo SSRC
    SsrcMapping *mapping = NULL;
    for (int i = 0; i < audio_count; i++) {
        if (audio_mappings[i].ssrc == (int)ssrc && audio_mappings[i].active) {
            mapping = &audio_mappings[i];
            break;
        }
    }

    if (!mapping) {
        printf(" Audio SSRC %u not registered, ignoring\n", ssrc);
        // unlock
        pthread_mutex_unlock(&audio_mutex);
        return;
    }

    // printf(" mapping: %s -> %s:%d\n",
    //        mapping->sessionId, destination_host, mapping->destinationPort);

    // Crea elementi dinamicamente
    GstElement *queue = gst_element_factory_make("queue", NULL);
    GstElement *sink = gst_element_factory_make("udpsink", NULL);

    if (!queue || !sink) {
        fprintf(stderr, " Failed to create queue/sink for audio SSRC %u\n", ssrc);
        if (queue)
            gst_object_unref(queue);
        if (sink)
            gst_object_unref(sink);
        // unlock
        pthread_mutex_unlock(&audio_mutex);
        return;
    }

    // Config udpsink
    g_object_set(sink,
                 "host", destination_host,
                 "port", mapping->destinationPort,
                 "sync", FALSE,
                 "async", FALSE,
                 NULL);

    // Aggiungi a pipeline
    gst_bin_add_many(GST_BIN(audio_pipeline), queue, sink, NULL);

    // Link: demux pad -> queue
    GstPad *queue_sink_pad = gst_element_get_static_pad(queue, "sink");
    // colleghiamo pad legato a ssrc alla sua coda
    GstPadLinkReturn link_ret = gst_pad_link(pad, queue_sink_pad);
    // rilasciamo riferimento a pad coda
    gst_object_unref(queue_sink_pad);

    // check
    if (link_ret != GST_PAD_LINK_OK) {
        fprintf(stderr, " Failed to link demux pad to queue for audio SSRC %u\n", ssrc);
        gst_bin_remove_many(GST_BIN(audio_pipeline), queue, sink, NULL);
        gst_object_unref(queue);
        gst_object_unref(sink);
        // unlock
        pthread_mutex_unlock(&audio_mutex);
        return;
    }

    // Link: queue -> udpsink, colleghiamo coda a uscita finale, questo è più semplice perchè rapporto 1 a 1
    if (!gst_element_link(queue, sink)) {
        fprintf(stderr, " Failed to link queue to sink for audio SSRC %u\n", ssrc);
        gst_bin_remove_many(GST_BIN(audio_pipeline), queue, sink, NULL);
        gst_object_unref(queue);
        gst_object_unref(sink);
        // unlock
        pthread_mutex_unlock(&audio_mutex);
        return;
    }

    // Sincronizza stato con parent pipeline
    gst_element_sync_state_with_parent(queue);
    gst_element_sync_state_with_parent(sink);

    // Salva riferimenti nel mapping
    mapping->queue = queue;
    mapping->udpsink = sink;
    mapping->demuxPad = pad;

    // unlock
    pthread_mutex_unlock(&audio_mutex);

    printf(" Audio SSRC %u linked to %s:%d\n",
           ssrc, destination_host, mapping->destinationPort);
}

// stesse considerazioni di funzione precedente
void on_video_pad_added(GstElement *demux, guint ssrc, GstPad *pad, gpointer user_data) {
    (void)demux;
    (void)user_data;

    printf(" New video SSRC detected: %u\n", ssrc);

    // lock
    pthread_mutex_lock(&video_mutex);

    // Cerca mapping per questo SSRC
    SsrcMapping *mapping = NULL;
    for (int i = 0; i < video_count; i++) {
        if (video_mappings[i].ssrc == (int)ssrc && video_mappings[i].active) {
            mapping = &video_mappings[i];
            break;
        }
    }

    if (!mapping) {
        printf(" Video SSRC %u not registered, ignoring\n", ssrc);
        // unlock
        pthread_mutex_unlock(&video_mutex);
        return;
    }

    printf(" Found mapping: %s -> %s:%d\n",
           mapping->sessionId, destination_host, mapping->destinationPort);

    // Crea elementi dinamicamente
    GstElement *queue = gst_element_factory_make("queue", NULL);
    GstElement *sink = gst_element_factory_make("udpsink", NULL);

    if (!queue || !sink) {
        fprintf(stderr, " Failed to create queue/sink for video SSRC %u\n", ssrc);
        if (queue)
            gst_object_unref(queue);
        if (sink)
            gst_object_unref(sink);
        // unlock
        pthread_mutex_unlock(&video_mutex);
        return;
    }

    // Config udpsink
    g_object_set(sink,
                 "host", destination_host,
                 "port", mapping->destinationPort,
                 "sync", FALSE,
                 "async", FALSE,
                 NULL);

    // Aggiungi alla pipeline
    gst_bin_add_many(GST_BIN(video_pipeline), queue, sink, NULL);

    // Link: demux pad -> queue
    GstPad *queue_sink_pad = gst_element_get_static_pad(queue, "sink");
    GstPadLinkReturn link_ret = gst_pad_link(pad, queue_sink_pad);
    gst_object_unref(queue_sink_pad);

    if (link_ret != GST_PAD_LINK_OK) {
        fprintf(stderr, " Failed to link demux pad to queue for video SSRC %u\n", ssrc);
        gst_bin_remove_many(GST_BIN(video_pipeline), queue, sink, NULL);
        gst_object_unref(queue);
        gst_object_unref(sink);

        // unlock
        pthread_mutex_unlock(&video_mutex);
        return;
    }

    // Link: queue -> udpsink
    if (!gst_element_link(queue, sink)) {
        fprintf(stderr, " Failed to link queue to sink for video SSRC %u\n", ssrc);
        gst_bin_remove_many(GST_BIN(video_pipeline), queue, sink, NULL);
        gst_object_unref(queue);
        gst_object_unref(sink);

        // unlock
        pthread_mutex_unlock(&video_mutex);
        return;
    }

    // Sincronizza stato con parent
    gst_element_sync_state_with_parent(queue);
    gst_element_sync_state_with_parent(sink);

    // Salva riferimenti nel mapping
    mapping->queue = queue;
    mapping->udpsink = sink;
    mapping->demuxPad = pad;

    // unlock
    pthread_mutex_unlock(&video_mutex);
    printf(" Video SSRC %u linked to %s:%d\n",
           ssrc, destination_host, mapping->destinationPort);
}

// GSTREAMER SETUP

int setup_pipelines(int audio_port, int video_port, const char *dest_host) {
    printf(" Setting up GStreamer pipelines with rtpssrcdemux...\n");

    // Salva destination host
    strncpy(destination_host, dest_host, sizeof(destination_host) - 1);

    // AUDIO PIPELINE
    audio_pipeline = gst_pipeline_new("audio-pipeline");

    GstElement *audio_src = gst_element_factory_make("udpsrc", "audio-src");
    audio_demux = gst_element_factory_make("rtpssrcdemux", "audio-demux");

    if (!audio_pipeline || !audio_src || !audio_demux) {
        fprintf(stderr, " Failed to create audio pipeline elements\n");
        return -1;
    }

    // Config udpsrc
    g_object_set(audio_src,
                 "port", audio_port,
                 "caps", gst_caps_from_string("application/x-rtp"),
                 NULL);

    // Connetti callback per dynamic pad
    g_signal_connect(audio_demux, "new-ssrc-pad",
                     G_CALLBACK(on_audio_pad_added), NULL);

    // Build pipeline
    gst_bin_add_many(GST_BIN(audio_pipeline), audio_src, audio_demux, NULL);

    if (!gst_element_link(audio_src, audio_demux)) {
        fprintf(stderr, " Failed to link audio src to demux\n");
        return -1;
    }

    // Avvia pipeline
    gst_element_set_state(audio_pipeline, GST_STATE_PAUSED);

    printf(" Audio pipeline ready (port %d) with dynamic demux\n", audio_port);

    // VIDEO PIPELINE
    video_pipeline = gst_pipeline_new("video-pipeline");

    GstElement *video_src = gst_element_factory_make("udpsrc", "video-src");
    video_demux = gst_element_factory_make("rtpssrcdemux", "video-demux");

    if (!video_pipeline || !video_src || !video_demux) {
        fprintf(stderr, " Failed to create video pipeline elements\n");
        return -1;
    }

    // Config udpsrc
    g_object_set(video_src,
                 "port", video_port,
                 "caps", gst_caps_from_string("application/x-rtp"),
                 NULL);

    // Connetti callback per dynamic pad
    g_signal_connect(video_demux, "new-ssrc-pad",
                     G_CALLBACK(on_video_pad_added), NULL);

    // Build pipeline
    gst_bin_add_many(GST_BIN(video_pipeline), video_src, video_demux, NULL);

    if (!gst_element_link(video_src, video_demux)) {
        fprintf(stderr, " Failed to link video src to demux\n");
        return -1;
    }

    // Avvia pipeline
    gst_element_set_state(video_pipeline, GST_STATE_PAUSED);

    printf(" Video pipeline ready (port %d) with dynamic demux\n", video_port);
    return 0;
}

// COMMAND HANDLERS

void handle_add(const char *sessionId, int audioSsrc, int videoSsrc,
                int audioPort, int videoPort) {

    printf(" ADD mountpoint: %s\n", sessionId);
    printf(" Audio: SSRC=%d -> port=%d\n", audioSsrc, audioPort);
    printf(" Video: SSRC=%d -> port=%d\n", videoSsrc, videoPort);

    // lock
    pthread_mutex_lock(&audio_mutex);

    // Verifica limiti
    if (audio_count >= MAX_MOUNTPOINTS) {
        fprintf(stderr, " Max mountpoints reached (audio)\n");
        write(client_connection, "ERROR: Max mountpoints\n", 23);

        // unlock
        pthread_mutex_unlock(&audio_mutex);
        return;
    }

    // Registra mapping audio
    SsrcMapping *audio_map = &audio_mappings[audio_count];
    strncpy(audio_map->sessionId, sessionId, sizeof(audio_map->sessionId) - 1);
    audio_map->ssrc = audioSsrc;
    audio_map->destinationPort = audioPort;
    audio_map->queue = NULL;
    audio_map->udpsink = NULL;
    audio_map->demuxPad = NULL;
    audio_map->active = 1;
    audio_count++;

    // unlock
    pthread_mutex_unlock(&audio_mutex);

    // lock
    pthread_mutex_lock(&video_mutex);

    if (video_count >= MAX_MOUNTPOINTS) {
        fprintf(stderr, " Max mountpoints reached (video)\n");

        // unlock
        pthread_mutex_unlock(&video_mutex);
        write(client_connection, "ERROR: Max mountpoints\n", 23);
        return;
    }

    // Registra mapping video
    SsrcMapping *video_map = &video_mappings[video_count];
    strncpy(video_map->sessionId, sessionId, sizeof(video_map->sessionId) - 1);
    video_map->ssrc = videoSsrc;
    video_map->destinationPort = videoPort;
    video_map->queue = NULL;
    video_map->udpsink = NULL;
    video_map->demuxPad = NULL;
    video_map->active = 1;
    video_count++;

    // unlock
    pthread_mutex_unlock(&video_mutex);

    printf(" Mountpoint registered (waiting for RTP...)\n");

    write(client_connection, "OK\n", 3);
}

void handle_remove(const char *sessionId) {
    printf(" REMOVE mountpoint: %s\n", sessionId);
    int found = 0;

    // RIMUOVI AUDIO
    pthread_mutex_lock(&audio_mutex);
    for (int i = 0; i < audio_count; i++) {
        if (strcmp(audio_mappings[i].sessionId, sessionId) == 0) {
            SsrcMapping *mapping = &audio_mappings[i];

            if (mapping->queue && mapping->udpsink) {
                printf(" Unlinking audio SSRC %d\n", mapping->ssrc);

                // Blocca gli elementi
                gst_element_set_state(mapping->queue, GST_STATE_NULL);
                gst_element_set_state(mapping->udpsink, GST_STATE_NULL);

                // Unlink il pad
                if (mapping->demuxPad) {
                    GstPad *queue_sink = gst_element_get_static_pad(mapping->queue, "sink");
                    if (queue_sink) {
                        if (gst_pad_is_linked(mapping->demuxPad)) {
                            gst_pad_unlink(mapping->demuxPad, queue_sink);
                        }
                        gst_object_unref(queue_sink);
                    }
                }

                // Rimuovi elementi dal bin
                gst_bin_remove_many(GST_BIN(audio_pipeline),
                                    mapping->queue, mapping->udpsink, NULL);

                // Unref elementi
                gst_object_unref(mapping->queue);
                gst_object_unref(mapping->udpsink);

                // Rimuovi il pad dal demux DOPO aver scollegato tutto
                if (audio_demux && mapping->demuxPad) {
                    printf(" Clearing SSRC %d from audio demux\n", mapping->ssrc);
                    g_signal_emit_by_name(audio_demux, "clear-ssrc", (guint)mapping->ssrc);
                }

                mapping->queue = NULL;
                mapping->udpsink = NULL;
                mapping->demuxPad = NULL;
            }

            // Rimuovi dalla lista
            if (i != audio_count - 1) {
                audio_mappings[i] = audio_mappings[audio_count - 1];
            }
            audio_count--;
            found = 1;
            break;
        }
    }
    pthread_mutex_unlock(&audio_mutex);

    // RIMUOVI VIDEO
    pthread_mutex_lock(&video_mutex);
    for (int i = 0; i < video_count; i++) {
        if (strcmp(video_mappings[i].sessionId, sessionId) == 0) {
            SsrcMapping *mapping = &video_mappings[i];

            if (mapping->queue && mapping->udpsink) {
                printf(" Unlinking video SSRC %d\n", mapping->ssrc);

                // Blocca elementi
                gst_element_set_state(mapping->queue, GST_STATE_NULL);
                gst_element_set_state(mapping->udpsink, GST_STATE_NULL);

                // Unlink pad
                if (mapping->demuxPad) {
                    GstPad *queue_sink = gst_element_get_static_pad(mapping->queue, "sink");
                    if (queue_sink) {
                        if (gst_pad_is_linked(mapping->demuxPad)) {
                            gst_pad_unlink(mapping->demuxPad, queue_sink);
                        }
                        gst_object_unref(queue_sink);
                    }
                }

                // Rimuovi dal pipeline
                gst_bin_remove_many(GST_BIN(video_pipeline),
                                    mapping->queue, mapping->udpsink, NULL);

                // Unref elementi
                gst_object_unref(mapping->queue);
                gst_object_unref(mapping->udpsink);

                // Rimuovi il pad dal demux
                if (video_demux && mapping->demuxPad) {
                    printf(" Clearing SSRC %d from video demux\n", mapping->ssrc);
                    g_signal_emit_by_name(video_demux, "clear-ssrc", (guint)mapping->ssrc);
                }

                mapping->queue = NULL;
                mapping->udpsink = NULL;
                mapping->demuxPad = NULL;
            }

            // Rimuovi dalla lista
            if (i != video_count - 1) {
                video_mappings[i] = video_mappings[video_count - 1];
            }
            video_count--;
            found = 1;
            break;
        }
    }
    pthread_mutex_unlock(&video_mutex);

    if (found) {
        printf(" Mountpoint removed: %s\n", sessionId);
        write(client_connection, "OK\n", 3);
    } else {
        printf(" Mountpoint not found: %s\n", sessionId);
        write(client_connection, "ERROR: Not found\n", 17);
    }
}
// SOCKET COMMAND LOOP

void *command_loop(void) {
    char buffer[BUFFER_SIZE];
    char c;

    printf(" Command loop ready\n");

    while (1) {
        int i = 0;
        ssize_t n;

        // Leggi carattere per carattere fino a '\n'
        while (i < BUFFER_SIZE - 1) {
            n = read(client_connection, &c, 1);

            // Gestione disconnessione/errori
            if (n <= 0) {
                printf(" Client disconnected\n");
                close(client_connection);

                printf(" Waiting for client reconnection...\n");

                // Aspetta nuova connessione
                client_connection = accept(server_socket, NULL, NULL);

                if (client_connection < 0) {
                    perror(" accept() failed during reconnection");
                    cleanup_and_exit(1);
                }

                printf(" Client reconnected!\n");

                i = 0;
                continue;
            }

            if (c == '\n')
                break;
            buffer[i++] = c;
        }

        buffer[i] = '\0';

        // Ignora linea vuota
        if (strlen(buffer) == 0)
            continue;

        printf(" Received: '%s'\n", buffer);

        // GESTIONE COMANDO

        char cmd[64], sessionId[64];
        int audioSsrc, videoSsrc, audioPort, videoPort;

        // ADD <sessionId> <audioSsrc> <videoSsrc> <audioPort> <videoPort>
        if (sscanf(buffer, "%s %s %d %d %d %d",
                   cmd, sessionId, &audioSsrc, &videoSsrc, &audioPort, &videoPort) == 6) {

            if (strcmp(cmd, "ADD") == 0) {
                handle_add(sessionId, audioSsrc, videoSsrc, audioPort, videoPort);
            } else {
                write(client_connection, "ERROR: Unknown command\n", 23);
            }
        }
        // REMOVE <sessionId>
        else if (sscanf(buffer, "%s %s", cmd, sessionId) == 2) {
            if (strcmp(cmd, "REMOVE") == 0) {
                handle_remove(sessionId);
            } else {
                write(client_connection, "ERROR: Unknown command\n", 23);
            }
        }
        // PING
        else if (strcmp(buffer, "PING") == 0) {
            write(client_connection, "PONG\n", 5);
        }
        // LIST
        else if (strcmp(buffer, "LIST") == 0) {
            // Crea oggetto JSON root
            cJSON *root = cJSON_CreateObject();
            cJSON *audio_array = cJSON_CreateArray();
            cJSON *video_array = cJSON_CreateArray();

            // Lock e aggiungi audio mappings
            pthread_mutex_lock(&audio_mutex);
            for (int i = 0; i < audio_count; i++) {
                cJSON *mapping = cJSON_CreateObject();
                cJSON_AddStringToObject(mapping, "sessionId", audio_mappings[i].sessionId);
                cJSON_AddNumberToObject(mapping, "ssrc", audio_mappings[i].ssrc);
                cJSON_AddNumberToObject(mapping, "port", audio_mappings[i].destinationPort);
                cJSON_AddBoolToObject(mapping, "linked", audio_mappings[i].queue != NULL);
                cJSON_AddItemToArray(audio_array, mapping);
            }
            pthread_mutex_unlock(&audio_mutex);

            // Lock e aggiungi video mappings
            pthread_mutex_lock(&video_mutex);
            for (int i = 0; i < video_count; i++) {
                cJSON *mapping = cJSON_CreateObject();
                cJSON_AddStringToObject(mapping, "sessionId", video_mappings[i].sessionId);
                cJSON_AddNumberToObject(mapping, "ssrc", video_mappings[i].ssrc);
                cJSON_AddNumberToObject(mapping, "port", video_mappings[i].destinationPort);
                cJSON_AddBoolToObject(mapping, "linked", video_mappings[i].queue != NULL);
                cJSON_AddItemToArray(video_array, mapping);
            }
            pthread_mutex_unlock(&video_mutex);

            // Aggiungi array al root
            cJSON_AddItemToObject(root, "audio", audio_array);
            cJSON_AddItemToObject(root, "video", video_array);

            // Stringify e invia
            char *json_string = cJSON_PrintUnformatted(root);
            if (json_string) {
                write(client_connection, json_string, strlen(json_string));
                write(client_connection, "\n", 1);
                write(client_connection, "END\n", 4);
                free(json_string);
            } else {
                write(client_connection, "ERROR: JSON creation failed\n", 28);
            }

            // Cleanup
            cJSON_Delete(root);
        }
        // SHUTDOWN
        else if (strcmp(buffer, "SHUTDOWN") == 0) {
            write(client_connection, "BYE\n", 4);
            printf(" Shutdown requested\n");
            cleanup_and_exit(0);
            return NULL;
        } else if (strcmp(buffer, "PLAY") == 0) {
            // Metti pipeline in PLAYING
            if (audio_pipeline) {
                GstStateChangeReturn ret = gst_element_set_state(audio_pipeline, GST_STATE_PLAYING);
                if (ret == GST_STATE_CHANGE_FAILURE) {
                    fprintf(stderr, " Failed to set audio pipeline to PLAYING\n");
                    write(client_connection, "ERROR: Audio pipeline failed\n", 29);
                    continue;
                }
                printf(" Audio pipeline -> PLAYING\n");
            }

            if (video_pipeline) {
                GstStateChangeReturn ret = gst_element_set_state(video_pipeline, GST_STATE_PLAYING);
                if (ret == GST_STATE_CHANGE_FAILURE) {
                    fprintf(stderr, " Failed to set video pipeline to PLAYING\n");
                    write(client_connection, "ERROR: Video pipeline failed\n", 29);
                    continue;
                }
                printf(" Video pipeline -> PLAYING\n");
            }
            write(client_connection, "OK\n", 3);
        } else {
            write(client_connection, "ERROR: Invalid format\n", 22);
        }
    }
}

// MAIN

int main(int argc, char *argv[]) {
    if (argc < 5) {
        fprintf(stderr, "Usage: %s <nodeId> <audioPort> <videoPort> <destinationHost>\n", argv[0]);
        fprintf(stderr, "Example: %s egress-1 5002 5004 janus-streaming-1\n", argv[0]);
        return 1;
    }

    const char *node_id = argv[1];
    int audio_port = atoi(argv[2]);
    int video_port = atoi(argv[3]);
    const char *dest_host = argv[4];

    // Disabilita buffering per vedere log in tempo reale
    setbuf(stdout, NULL);
    setbuf(stderr, NULL);

    // Setup signal handlers
    signal(SIGTERM, cleanup_and_exit);
    signal(SIGINT, cleanup_and_exit);

    // Costruisci path socket
    snprintf(socket_path, sizeof(socket_path),
             "/tmp/egress-forwarder-%s.sock", node_id);

    printf(" Starting egress-forwarder for node: %s\n", node_id);
    printf(" Socket path: %s\n", socket_path);
    printf(" Audio port: %d\n", audio_port);
    printf(" Video port: %d\n", video_port);
    printf(" Destination: %s\n", dest_host);

    // Inizializza GStreamer
    gst_init(&argc, &argv);

    // Setup pipelines
    if (setup_pipelines(audio_port, video_port, dest_host) < 0) {
        return 1;
    }

    // Rimuovi socket esistente se presente
    unlink(socket_path);

    // Crea socket Unix
    server_socket = socket(AF_UNIX, SOCK_STREAM, 0);
    if (server_socket < 0) {
        perror(" socket() failed");
        return 1;
    }

    // Bind al path
    struct sockaddr_un addr;
    memset(&addr, 0, sizeof(addr));
    addr.sun_family = AF_UNIX;
    strncpy(addr.sun_path, socket_path, sizeof(addr.sun_path) - 1);

    if (bind(server_socket, (struct sockaddr *)&addr, sizeof(addr)) < 0) {
        perror(" bind() failed");
        close(server_socket);
        return 1;
    }

    // Socket in ascolto
    if (listen(server_socket, 1) < 0) {
        perror(" listen() failed");
        cleanup_and_exit(0);
        return 1;
    }

    printf(" Waiting for connection...\n");

    // Accept (blocca finché Node.js non si connette)
    client_connection = accept(server_socket, NULL, NULL);
    if (client_connection < 0) {
        perror(" accept() failed");
        cleanup_and_exit(0);
        return 1;
    }

    printf(" Client connected!\n");

    // Command loop
    command_loop();

    return 0;
}