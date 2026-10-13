(ns webapp.audit.views.terminal-decoder)

(defn b64->bytes [b64]
  (let [bin (js/atob b64)
        out (js/Uint8Array. (.-length bin))]
    (dotimes [i (.-length bin)]
      (aset out i (.charCodeAt bin i)))
    out))

(defn decode-byte-events
  "Decode an ordered stream of [seconds type Uint8Array] events. PTY
  output/error share one byte stream; input is a separate stream and must not
  complete an output code point. Keep an unfinished code point buffered while
  the stream is live. Resize events (\"r\") carry text and pass through."
  ([events] (decode-byte-events events true))
  ([events finalize?]
   (let [output-decoder (js/TextDecoder. "utf-8")
         input-decoder (js/TextDecoder. "utf-8")]
     (loop [events (seq events)
            decoded []
            last-output nil
            last-input nil]
       (if-let [[seconds event-type data :as event] (first events)]
         (if (= "r" event-type)
           (recur (next events) (conj decoded event) last-output last-input)
           (let [output? (contains? #{"o" "e"} event-type)
                 decoder (if output? output-decoder input-decoder)
                 text (.decode decoder data #js {:stream true})
                 idx (count decoded)]
             (recur (next events)
                    (conj decoded [seconds event-type text])
                    (if output? idx last-output)
                    (if output? last-input idx))))
         (if finalize?
           (let [output-end (.decode output-decoder)
                 input-end (.decode input-decoder)]
             (cond-> decoded
               (and (some? last-output) (seq output-end))
               (update-in [last-output 2] str output-end)
               (and (some? last-input) (seq input-end))
               (update-in [last-input 2] str input-end)))
           decoded))))))

(defn decode-events
  "decode-byte-events over an audit stream of [seconds type base64] events."
  ([event-stream] (decode-events event-stream true))
  ([event-stream finalize?]
   (decode-byte-events (map (fn [[seconds event-type b64]]
                              [seconds event-type (b64->bytes b64)])
                            event-stream)
                       finalize?)))
