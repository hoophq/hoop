(ns webapp.audit.views.terminal-decoder)

(defn- b64->bytes [b64]
  (let [bin (js/atob b64)
        out (js/Uint8Array. (.-length bin))]
    (dotimes [i (.-length bin)]
      (aset out i (.charCodeAt bin i)))
    out))

(defn decode-events
  "Decode an ordered audit stream. PTY output/error share one byte stream;
  input is a separate stream and must not complete an output code point.
  Keep an unfinished code point buffered while the stream is live."
  ([event-stream] (decode-events event-stream true))
  ([event-stream finalize?]
   (let [output-decoder (js/TextDecoder. "utf-8")
         input-decoder (js/TextDecoder. "utf-8")]
     (loop [events (seq event-stream)
            decoded []
            last-output nil
            last-input nil]
       (if-let [[seconds event-type b64] (first events)]
         (let [output? (contains? #{"o" "e"} event-type)
               decoder (if output? output-decoder input-decoder)
               text (.decode decoder (b64->bytes b64) #js {:stream true})
               idx (count decoded)]
           (recur (next events)
                  (conj decoded [seconds event-type text])
                  (if output? idx last-output)
                  (if output? last-input idx)))
         (if finalize?
           (let [output-end (.decode output-decoder)
                 input-end (.decode input-decoder)]
             (cond-> decoded
               (and (some? last-output) (seq output-end))
               (update-in [last-output 2] str output-end)
               (and (some? last-input) (seq input-end))
               (update-in [last-input 2] str input-end)))
           decoded))))))
