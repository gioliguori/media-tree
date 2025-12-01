CC = gcc
# Flags di compilazione
# -Wall -Wextra = warning
# $(shell pkg-config ...) = path include di GStreamer
CFLAGS = -Wall -Wextra $(shell pkg-config --cflags gstreamer-1.0)
# librerie di gstreamer
LIBS = $(shell pkg-config --libs gstreamer-1.0)  -lpthread -lcjson

TARGET = relay-forwarder
SRC = relay-forwarder.c

all: $(TARGET)

$(TARGET): $(SRC)
	$(CC) $(SRC) -o $(TARGET) $(CFLAGS) $(LIBS)

clean:
	rm -f $(TARGET)

# comandi
.PHONY: all clean