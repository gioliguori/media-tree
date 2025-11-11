#include <gst/gst.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h> // socket
#include <sys/un.h>     // socket unix
#include <unistd.h>

#define BUFFER_SIZE 256

// forse basta una sola
int server_socket = -1;     // socket file descriptor    ACCETTARE connessioni
int client_connection = -1; // client file descriptor (nodejs)  COMUNICARE con il client
char socket_path[256];      // (es: /tmp/relay-forwarder-relay-1.sock)

// flag cleanup
static int cleaning = 0;

// GStreamer
GstElement *audio_pipeline = NULL;
GstElement *video_pipeline = NULL;
GstElement *audio_sink = NULL;
GstElement *video_sink = NULL;

void cleanup_and_exit(int sig) {
    (void)sig; // evita warning

    if (cleaning) {
        return;
    }
    cleaning = 1;

    printf(" Cleaning up...\n");

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

// GSTREAMER SETUP

// Crea e avvia due pipeline: audio e video
// Pipeline: udpsrc (riceve RTP) -> multiudpsink (invia a N destinazioni)
int setup_pipelines(int audio_port, int video_port) {
    printf(" Setting up GStreamer pipelines...\n");

    // Audio pipeline: udpsrc -> multiudpsink
    audio_pipeline = gst_pipeline_new("audio-pipeline");
    // - udpsrc: riceve pacchetti UDP RTP sulla porta audio_port
    // - multiudpsink: invia a multiple destinazioni (gestito da ADD/REMOVE)
    GstElement *audio_src = gst_element_factory_make("udpsrc", "audio-src");
    audio_sink = gst_element_factory_make("multiudpsink", "audio-sink");

    if (!audio_pipeline || !audio_src || !audio_sink) {
        fprintf(stderr, " Failed to create audio pipeline elements\n");
        return -1;
    }

    // configura porta
    g_object_set(audio_src,
                 "port", audio_port,
                 "caps", gst_caps_from_string("application/x-rtp"), // solo rtp
                 // "buffer-size", 2097152, non so
                 NULL);

    // no synch no buffering, facciamo solo forwarding
    g_object_set(audio_sink,
                 "sync", FALSE,
                 "async", FALSE,
                 NULL);

    // Build audio pipeline
    gst_bin_add_many(GST_BIN(audio_pipeline), audio_src, audio_sink, NULL);

    // link logico tra i due elementi
    if (!gst_element_link(audio_src, audio_sink)) {
        fprintf(stderr, " Failed to link audio elements\n");
        return -1;
    }

    printf(" Audio pipeline ready (port %d)\n", audio_port);

    // Video pipeline: udpsrc -> multiudpsink
    video_pipeline = gst_pipeline_new("video-pipeline");
    // - udpsrc: riceve pacchetti UDP RTP sulla porta audio_port
    // - multiudpsink: invia a multiple destinazioni (gestito da ADD/REMOVE)
    GstElement *video_src = gst_element_factory_make("udpsrc", "video-src");
    video_sink = gst_element_factory_make("multiudpsink", "video-sink");

    if (!video_pipeline || !video_src || !video_sink) {
        fprintf(stderr, " Failed to create video pipeline elements\n");
        return -1;
    }

    // configura porta
    g_object_set(video_src,
                 "port", video_port,
                 "caps", gst_caps_from_string("application/x-rtp"),
                 // "buffer-size", 4194304, non so
                 NULL);

    // no synch no buffering, facciamo solo forwarding
    g_object_set(video_sink,
                 "sync", FALSE,
                 "async", FALSE,
                 NULL);

    // Build video pipeline
    gst_bin_add_many(GST_BIN(video_pipeline), video_src, video_sink, NULL);

    // link logico tra i due elementi (unidirezionale)
    if (!gst_element_link(video_src, video_sink)) {
        fprintf(stderr, " Failed to link video elements\n");
        return -1;
    }

    printf(" Video pipeline ready (port %d)\n", video_port);

    // Avvia le pipeline
    gst_element_set_state(audio_pipeline, GST_STATE_PLAYING);
    gst_element_set_state(video_pipeline, GST_STATE_PLAYING);

    // printf(" Pipelines PLAYING\n");
    return 0;
}

// COMMAND HANDLERS

// Gestisce comando: ADD <host> <audioPort> <videoPort>
// Aggiunge una destinazione al multiudpsink
void handle_add(const char *host, int audio_port, int video_port) {
    printf(" ADD destination: %s (audio=%d, video=%d)\n",
           host, audio_port, video_port);

    // Add to audio multiudpsink
    g_signal_emit_by_name(audio_sink, "add", host, audio_port, NULL);

    // Add to video multiudpsink
    g_signal_emit_by_name(video_sink, "add", host, video_port, NULL);

    printf(" Destination added\n");
}

// Gestisce comando: REMOVE <host> <audioPort> <videoPort>
// Rimuove destinazione dal multiudpsink
void handle_remove(const char *host, int audio_port, int video_port) {
    printf(" REMOVE destination: %s (audio=%d, video=%d)\n",
           host, audio_port, video_port);

    // Remove from audio multiudpsink
    g_signal_emit_by_name(audio_sink, "remove", host, audio_port, NULL);

    // Remove from video multiudpsink
    g_signal_emit_by_name(video_sink, "remove", host, video_port, NULL);

    printf(" Destination removed\n");
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

        char cmd[64], host[64];
        int audio_port = 0, video_port = 0;

        // comando con 4 parametri
        if (sscanf(buffer, "%s %s %d %d", cmd, host, &audio_port, &video_port) == 4) {
            if (strcmp(cmd, "ADD") == 0) {
                handle_add(host, audio_port, video_port);
                // OK in risposta
                write(client_connection, "OK\n", 3);
            } else if (strcmp(cmd, "REMOVE") == 0) {
                handle_remove(host, audio_port, video_port);
                // OK in risposta
                write(client_connection, "OK\n", 3);
            } else {
                write(client_connection, "ERROR: Unknown command\n", 23);
            }
        }
        // altri comandi
        else if (strcmp(buffer, "PING") == 0) {
            write(client_connection, "PONG\n", 5);
        } else if (strcmp(buffer, "LIST") == 0) {
            gchar *audio_clients = NULL;
            gchar *video_clients = NULL;

            // lista clienti audio/video
            g_object_get(audio_sink, "clients", &audio_clients, NULL);
            g_object_get(video_sink, "clients", &video_clients, NULL);

            char response[2048];
            // formatta risposta
            snprintf(response, sizeof(response),
                     "AUDIO: %s\nVIDEO: %s\nEND\n", // END per terminare
                     audio_clients ? audio_clients : "(none)",
                     video_clients ? video_clients : "(none)");

            write(client_connection, response, strlen(response));

            // libera memoria
            if (audio_clients)
                g_free(audio_clients);
            if (video_clients)
                g_free(video_clients);

        } else if (strcmp(buffer, "SHUTDOWN") == 0) {
            write(client_connection, "BYE\n", 4);
            printf(" Shutdown requested\n");
            cleanup_and_exit(0);
            return NULL;
        } else {
            write(client_connection, "ERROR: Invalid format\n", 22);
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

    printf(" Waiting for connection...\n");

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