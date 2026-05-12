(ns webapp.audit.views.session-live-tail
  "Dedicated view for live machine sessions (identity_type=machine + status=open).

  Renders the event stream as a terminal-style tail: newest entries at the
  bottom, auto-scroll, status pill that reflects the SSE connection state, and
  a filter for noisy protocol frames. For postgres sessions we decode the wire
  protocol locally so each Parse/Query frame shows as readable SQL instead of
  binary noise."
  (:require
   ["@radix-ui/themes" :refer [Badge Box Flex IconButton Switch
                               Text TextField Tooltip]]
   ["lucide-react" :refer [ArrowDown Database Search Terminal Zap]]
   [clojure.string :as string]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.audit.views.empty-event-stream :as empty-event-stream]
   [webapp.audit.views.pg-wire :as pg-wire]
   [webapp.utilities :as utilities]))

;; ─── Helpers ───────────────────────────────────────────────────────────────

(defn- pad2 [n] (let [s (str n)] (if (< (count s) 2) (str "0" s) s)))
(defn- pad3 [n] (let [s (str n)]
                  (case (count s) 1 (str "00" s) 2 (str "0" s) s)))

(defn- format-relative
  "+MM:SS.mmm offset from session start, derived from the `seconds` value the
  backend uses for each event (offset in seconds)."
  [seconds]
  (let [total-ms (max 0 (js/Math.floor (* seconds 1000)))
        ms (mod total-ms 1000)
        total-s (js/Math.floor (/ total-ms 1000))
        s (mod total-s 60)
        m (js/Math.floor (/ total-s 60))]
    (str "+" (pad2 m) ":" (pad2 s) "." (pad3 ms))))

(defn- format-absolute [start-date seconds]
  (when start-date
    (let [start-ms (.getTime (js/Date. start-date))
          d (js/Date. (+ start-ms (js/Math.floor (* seconds 1000))))]
      (.toLocaleTimeString d))))

(defn- duration-since [start-date]
  (when start-date
    (let [start-ms (.getTime (js/Date. start-date))
          now-ms (.getTime (js/Date.))
          diff-s (max 0 (js/Math.floor (/ (- now-ms start-ms) 1000)))
          h (js/Math.floor (/ diff-s 3600))
          m (mod (js/Math.floor (/ diff-s 60)) 60)
          s (mod diff-s 60)]
      (cond
        (pos? h) (str h "h " m "m")
        (pos? m) (str m "m " s "s")
        :else (str s "s")))))

;; Each raw event_stream entry is `[seconds type base64-payload]`. We expand it
;; into one or more "rows" — postgres payloads can contain multiple PG frames
;; concatenated, and each frame becomes its own row.
(defn- expand-event-row [postgres? row-idx [seconds event-type b64]]
  (cond
    (and postgres? (= event-type "i"))
    (let [frames (pg-wire/parse-payload b64)]
      (if (seq frames)
        (map-indexed
         (fn [frame-idx frame]
           {:key (str row-idx "-" frame-idx)
            :seconds seconds
            :event-type event-type
            :pg-type (:type frame)
            :pg-type-name (:type-name frame)
            :sql (:sql frame)
            :kind (if (pg-wire/query-frame? frame) :query :protocol)})
         frames)
        [{:key (str row-idx)
          :seconds seconds
          :event-type event-type
          :kind :protocol
          :pg-type-name "raw"}]))

    :else
    [{:key (str row-idx)
      :seconds seconds
      :event-type event-type
      :kind (case event-type
              "o" :output
              "e" :error
              :raw)
      :text (utilities/decode-b64 b64)}]))

(defn- expand-stream [postgres? event-stream]
  (->> event-stream
       (map-indexed (fn [i row] (expand-event-row postgres? i row)))
       (apply concat)
       vec))

;; ─── Sub-components ────────────────────────────────────────────────────────

(defn- status-pill [state]
  (let [[color label dot-class]
        (case state
          :live       ["green" "Live"        "bg-[--green-9] animate-pulse"]
          :connecting ["amber" "Connecting"  "bg-[--amber-9] animate-pulse"]
          :ended      ["gray"  "Ended"       "bg-[--gray-9]"]
          :error      ["red"   "Stream lost" "bg-[--red-9]"]
          ["gray" "Idle" "bg-[--gray-9]"])]
    [:> Badge {:color color :variant "soft" :size "2"
               :aria-label (str "Stream status: " label)}
     [:> Flex {:align "center" :gap "2"}
      [:span {:class (str "inline-block w-2 h-2 rounded-full " dot-class)
              :aria-hidden true}]
      label]]))

(defn- header
  [{:keys [state postgres? rows-count query-count duration
           only-queries? on-toggle-only-queries
           search on-change-search]}]
  [:> Box {:class (str "sticky top-0 z-10 bg-[--gray-1] "
                       "border border-[--gray-a4] rounded-t-3 "
                       "px-radix-4 py-radix-3")}
   [:> Flex {:justify "between" :align "center" :wrap "wrap" :gap "3"}
    [:> Flex {:align "center" :gap "3" :wrap "wrap"}
     [status-pill state]
     (when postgres?
       [:> Flex {:align "center" :gap "1" :class "text-[--gray-11]"}
        [:> Database {:size 14}]
        [:> Text {:size "2" :weight "medium"}
         (str query-count " " (if (= 1 query-count) "query" "queries"))]])
     [:> Flex {:align "center" :gap "1" :class "text-[--gray-11]"}
      [:> Terminal {:size 14}]
      [:> Text {:size "2"}
       (str rows-count " " (if (= 1 rows-count) "event" "events"))]]
     (when duration
       [:> Text {:size "2" :class "text-[--gray-10]"}
        (str "· " duration " elapsed")])]

    [:> Flex {:align "center" :gap "3" :wrap "wrap"}
     [:div {:class "w-56"}
      [:> TextField.Root
       {:size "2"
        :placeholder "Filter events…"
        :value search
        :on-change #(on-change-search (.. % -target -value))
        :aria-label "Filter live events"}
       [:> TextField.Slot
        [:> Search {:size 14}]]]]

     (when postgres?
       [:> Tooltip {:content "Hide protocol frames (Bind, Describe, Execute, Sync)"}
        [:label {:class "flex items-center gap-2 cursor-pointer select-none"}
         [:> Switch {:size "1"
                     :checked (boolean only-queries?)
                     :on-checked-change on-toggle-only-queries
                     :aria-label "Only queries"}]
         [:> Text {:size "2" :class "text-[--gray-11]"}
          "Only queries"]]])]]])

(defn- query-row [{:keys [seconds sql pg-type-name absolute]}]
  [:> Flex {:gap "3" :align "start"
            :class (str "px-radix-4 py-radix-2 "
                        "border-b border-[--gray-a3] "
                        "hover:bg-[--gray-2]")}
   [:> Tooltip {:content (or absolute "")}
    [:> Text {:size "1" :weight "medium"
              :class (str "text-[--gray-10] tabular-nums "
                          "shrink-0 pt-1 w-20 font-mono")}
     (format-relative seconds)]]
   [:> Badge {:color "iris" :variant "soft" :size "1"
              :class "shrink-0 mt-[2px]"}
    pg-type-name]
   [:> Box {:class "min-w-0 grow"}
    [:pre {:class (str "whitespace-pre-wrap break-words "
                       "text-[12px] leading-relaxed "
                       "text-[--gray-12] font-mono m-0")}
     sql]]])

(defn- protocol-row [{:keys [seconds pg-type pg-type-name absolute]}]
  [:> Flex {:gap "3" :align "center"
            :class (str "px-radix-4 py-radix-1 "
                        "border-b border-[--gray-a3] "
                        "text-[--gray-10]")}
   [:> Tooltip {:content (or absolute "")}
    [:> Text {:size "1" :class "tabular-nums shrink-0 w-20 font-mono"}
     (format-relative seconds)]]
   [:> Flex {:align "center" :gap "2"}
    [:> Zap {:size 12 :class "text-[--gray-9]"}]
    [:> Text {:size "1" :class "italic"}
     (str pg-type-name (when pg-type (str " (" pg-type ")")))]]])

(defn- text-row [{:keys [seconds event-type text absolute]}]
  (let [tone (case event-type
               "o" "text-[--gray-12]"
               "e" "text-[--red-11]"
               "text-[--gray-11]")]
    [:> Flex {:gap "3" :align "start"
              :class (str "px-radix-4 py-radix-2 "
                          "border-b border-[--gray-a3] "
                          "hover:bg-[--gray-2]")}
     [:> Tooltip {:content (or absolute "")}
      [:> Text {:size "1" :class (str "text-[--gray-10] tabular-nums "
                                      "shrink-0 pt-1 w-20 font-mono")}
       (format-relative seconds)]]
     [:> Box {:class "min-w-0 grow"}
      [:pre {:class (str "whitespace-pre-wrap break-words "
                         "text-[12px] leading-relaxed font-mono m-0 "
                         tone)}
       text]]]))

(defn- waiting-banner [postgres?]
  [:> Flex {:align "center" :justify "center" :gap "2"
            :class "py-radix-6 text-[--gray-10] italic"}
   [:span {:class "inline-block w-2 h-2 rounded-full bg-[--green-9] animate-pulse"
           :aria-hidden true}]
   [:> Text {:size "2"}
    (if postgres?
      "Waiting for the next query…"
      "Waiting for events…")]])

(defn- jump-to-latest-button [on-click]
  [:> Box {:class "absolute bottom-3 right-3"}
   [:> IconButton {:size "3" :variant "solid" :color "iris"
                   :on-click on-click
                   :aria-label "Jump to latest"
                   :class "shadow-lg"}
    [:> ArrowDown {:size 18}]]])

;; ─── Main component ────────────────────────────────────────────────────────

(defn main
  "Live event stream for machine sessions.
  Receives the current session map and renders the live tail."
  [_session]
  (let [scroll-ref (r/atom nil)
        only-queries? (r/atom true)
        ;; Auto-scroll follows the tail by default. When the user scrolls
        ;; up to inspect older events we stop fighting them and surface a
        ;; "Jump to latest" affordance instead.
        user-scrolled-away? (r/atom false)
        search (r/atom "")
        tick (r/atom 0)
        ticker (js/setInterval #(swap! tick inc) 1000)
        scroll-to-bottom!
        (fn []
          (when-let [el @scroll-ref]
            (set! (.-scrollTop el) (.-scrollHeight el))))
        handle-scroll
        (fn [e]
          (let [el (.-target e)
                near-bottom? (< (- (.-scrollHeight el)
                                   (.-scrollTop el)
                                   (.-clientHeight el))
                                32)]
            (reset! user-scrolled-away? (not near-bottom?))))
        jump-to-latest!
        (fn []
          (reset! user-scrolled-away? false)
          (scroll-to-bottom!))]
    (r/create-class
     {:display-name "session-live-tail"

      :component-will-unmount
      (fn [_] (js/clearInterval ticker))

      :component-did-update
      (fn [_]
        (when-not @user-scrolled-away?
          (scroll-to-bottom!)))

      :component-did-mount
      (fn [_] (scroll-to-bottom!))

      :reagent-render
      (fn [session]
        @tick ;; keep "elapsed" fresh
        (let [start-date (:start_date session)
              connection-subtype (:connection_subtype session)
              postgres? (= connection-subtype "postgres")
              stream-state (or @(rf/subscribe
                                 [:audit->session-stream-state (:id session)])
                               :connecting)
              event-stream (or (:event_stream session) [])
              rows (expand-stream postgres? event-stream)
              query-count (count (filter #(= :query (:kind %)) rows))
              search-term (string/lower-case @search)
              filtered (cond->> rows
                         (and postgres? @only-queries?)
                         (filter #(= :query (:kind %)))
                         (seq search-term)
                         (filter
                          (fn [r]
                            (let [hay (str (or (:sql r) "") " "
                                           (or (:text r) "") " "
                                           (or (:pg-type-name r) ""))]
                              (string/includes? (string/lower-case hay)
                                                search-term)))))
              filtered (map #(assoc % :absolute
                                    (format-absolute start-date (:seconds %)))
                            filtered)]
          [:> Box {:class "relative"}
           [header {:state stream-state
                    :postgres? postgres?
                    :rows-count (count rows)
                    :query-count query-count
                    :duration (duration-since start-date)
                    :only-queries? @only-queries?
                    :on-toggle-only-queries #(reset! only-queries? %)
                    :search @search
                    :on-change-search #(reset! search %)}]
           [:div {:ref (fn [el] (reset! scroll-ref el))
                  :on-scroll handle-scroll
                  :class (str "border-l border-r border-b border-[--gray-a4] "
                              "rounded-b-3 bg-[--gray-1] "
                              "max-h-[60vh] min-h-[200px] overflow-y-auto")}
            (cond
              (and (empty? rows) (= stream-state :ended))
              [empty-event-stream/main]

              (empty? filtered)
              [waiting-banner postgres?]

              :else
              [:<>
               (doall
                (for [row filtered]
                  ^{:key (:key row)}
                  (case (:kind row)
                    :query    [query-row row]
                    :protocol [protocol-row row]
                    [text-row row])))
               (when (= stream-state :live)
                 [waiting-banner postgres?])])]
           (when (and @user-scrolled-away? (= stream-state :live))
             [jump-to-latest-button jump-to-latest!])]))})))
