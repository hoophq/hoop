(ns webapp.audit.views.ssh-decoder
  "Reads hoop SSH recordings (libhoop proxy/ssh/types). Each audit event holds
  one frame: a type byte, the channel id (uint16, little endian), then the
  payload. Input events come from the client, output events from the server.

  Channel ids restart at 1 on each SSH connection of a session, and the audit
  stream does not tell connections apart. Sequential connections are fine:
  each channel closes before its id opens again. An id that opens while it is
  still open means two connections overlap, and their bytes cannot be told
  apart; such a recording is not replayed as a terminal.

  A live stream can start after a channel was opened. Nothing in the stream
  then tells whether that channel is a terminal, so its bytes are not replayed
  as one; it still counts as open for the overlap check."
  (:require
   [webapp.audit.views.terminal-decoder :as terminal-decoder]))

(def ^:private open-channel 1)
(def ^:private channel-request 2)
(def ^:private channel-data 3)
(def ^:private close-channel 4)
(def ^:private last-frame-type 7)

(def ^:private default-size {:width 80 :height 24})

(defn- frame [^js data]
  (let [frame-type (when (>= (.-length data) 3) (aget data 0))]
    (when (and frame-type (<= open-channel frame-type last-frame-type))
      {:type frame-type
       :channel (bit-or (aget data 1) (bit-shift-left (aget data 2) 8))})))

(defn- nul-terminated
  "The text from offset to the next NUL and the offset after that NUL, or nil
  when no NUL follows."
  [^js data offset]
  (let [end (.indexOf data 0 offset)]
    (when (>= end 0)
      [(.apply js/String.fromCharCode nil (.subarray data offset end)) (inc end)])))

(defn- uint32 [^js data offset]
  (.getUint32 (js/DataView. (.-buffer data) (.-byteOffset data) (.-byteLength data))
              offset false))

(defn- terminal-size [width height]
  (when (and (pos? width) (pos? height))
    {:width width :height height}))

(defn- pty-req-size
  "Columns and rows of a pty-req payload (RFC 4254 6.2): string TERM, then
  uint32 columns and rows."
  [^js payload]
  (when (>= (.-length payload) 4)
    (let [cols-at (+ 4 (uint32 payload 0))]
      (when (<= (+ cols-at 8) (.-length payload))
        (terminal-size (uint32 payload cols-at) (uint32 payload (+ cols-at 4)))))))

(defn- window-change-size
  "Columns and rows of a window-change payload (RFC 4254 6.7)."
  [^js payload]
  (when (>= (.-length payload) 8)
    (terminal-size (uint32 payload 0) (uint32 payload 4))))

(defn- resize-event [seconds {:keys [width height]}]
  [seconds "r" (str width "x" height)])

(defn- note-unopened
  "Remember a channel that sends frames without an open frame in the stream,
  so that an open of the same id counts as an overlap until it closes."
  [state channel]
  (cond-> state
    (not (or (contains? (:channels state) channel)
             (contains? (:closed state) channel)))
    (update :unopened conj channel)))

(defn- on-request
  "Apply a channel request: pty-req makes a session channel a terminal;
  pty-req and window-change set the terminal size."
  [state seconds channel request ^js payload]
  (let [{:keys [session? pty?]} (get-in state [:channels channel])
        pty-req? (and session? (= "pty-req" request))
        size (cond
               pty-req? (or (pty-req-size payload) default-size)
               (and pty? (= "window-change" request)) (window-change-size payload))]
    (cond-> (note-unopened state channel)
      pty-req? (assoc-in [:channels channel :pty?] true)
      (and size (:size state)) (update :events conj (resize-event seconds size))
      (and size (nil? (:size state))) (assoc :size size))))

(defn- on-data [state seconds event-type ^js data channel]
  (cond-> (note-unopened state channel)
    (get-in state [:channels channel :pty?])
    (update :events conj [seconds event-type (.subarray data 3)])))

(defn- on-frame
  "Next state for one frame, or nil when the bytes are not a valid frame."
  [state seconds event-type ^js data {frame-type :type channel :channel}]
  (condp = frame-type
    open-channel
    (when-let [[channel-type] (nul-terminated data 3)]
      (cond-> (-> state
                  (assoc-in [:channels channel] {:session? (= "session" channel-type)})
                  (update :closed disj channel))
        (or (contains? (:channels state) channel)
            (contains? (:unopened state) channel))
        (assoc :overlapping? true)))

    channel-request
    (when-let [[request payload-at] (nul-terminated data 3)]
      ;; one want-reply byte follows the request name
      (on-request state seconds channel request (.subarray data (inc payload-at))))

    channel-data
    (on-data state seconds event-type data channel)

    close-channel
    (-> state
        (update :channels dissoc channel)
        (update :unopened disj channel)
        (update :closed conj channel))

    (note-unopened state channel)))

(defn terminal-recording
  "Read an audit stream of [seconds type base64] events as an SSH recording.
  Returns nil when the stream is not SSH frames: a session of a subtype that
  was a PTY connection when it was recorded. Otherwise returns
  {:terminal? :width :height :events}. terminal? is true when a channel is a
  terminal and no two connections overlap; events are [seconds type
  Uint8Array] for the data of terminal channels, the gateway's error events,
  and [seconds \"r\" \"COLSxROWS\"] for terminal size changes."
  [event-stream]
  (loop [events (seq event-stream)
         state {:channels {} :unopened #{} :closed #{} :events [] :size nil :framed? false}]
    (if-let [[seconds event-type b64] (first events)]
      (let [data (terminal-decoder/b64->bytes b64)]
        (cond
          (zero? (.-length data))
          (recur (next events) state)

          ;; The gateway writes its own error text when the session ends.
          (= "e" event-type)
          (recur (next events) (update state :events conj [seconds event-type data]))

          :else
          (when-let [next-state (some->> (frame data)
                                         (on-frame state seconds event-type data))]
            (recur (next events) (assoc next-state :framed? true)))))
      (when (:framed? state)
        (merge (or (:size state) default-size)
               ;; every pty-req sets the size
               {:terminal? (and (some? (:size state)) (not (:overlapping? state)))
                :events (:events state)})))))
