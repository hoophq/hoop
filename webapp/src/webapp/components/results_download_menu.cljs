(ns webapp.components.results-download-menu
  (:require
   ["@radix-ui/themes" :refer [DropdownMenu Flex IconButton Text]]
   ["lucide-react" :refer [MoreHorizontal FileText Sheet Braces
                           SquareArrowOutUpRight Copy]]
   ["papaparse" :as papa]
   [clojure.string :as cs]
   [re-frame.core :as rf]))

(def ^:private client-side-threshold (* 2 1024 1024))

(defn- pad2 [n] (if (< n 10) (str "0" n) (str n)))

(defn- filename-timestamp []
  (let [d (js/Date.)]
    (str (.getUTCFullYear d)
         (pad2 (inc (.getUTCMonth d)))
         (pad2 (.getUTCDate d))
         "-"
         (pad2 (.getUTCHours d))
         (pad2 (.getUTCMinutes d))
         (pad2 (.getUTCSeconds d)))))

(defn- build-filename [{:keys [connection-name session-id]} ext]
  (let [parts (cond-> []
                (and connection-name (not (cs/blank? connection-name))) (conj connection-name)
                (and session-id (not (cs/blank? session-id))) (conj session-id)
                true (conj (filename-timestamp)))]
    (str (cs/join "-" parts) "." ext)))

(defn- trigger-download! [filename mime-type content]
  (let [blob (js/Blob. #js [content] #js {:type mime-type})
        url (js/URL.createObjectURL blob)
        a (.createElement js/document "a")]
    (set! (.-href a) url)
    (set! (.-download a) filename)
    (.appendChild js/document.body a)
    (.click a)
    (.removeChild js/document.body a)
    (js/setTimeout #(js/URL.revokeObjectURL url) 0)))

(defn- matrix->csv [matrix]
  (papa/unparse (clj->js matrix)))

(defn- matrix->json [heads body]
  (let [head-keys (vec (map-indexed
                        (fn [idx h]
                          (if (or (nil? h) (cs/blank? h))
                            (str "column_" (inc idx))
                            h))
                        heads))
        rows (mapv (fn [row]
                     (into {}
                           (map vector head-keys row)))
                   body)]
    (.stringify js/JSON (clj->js rows) nil 2)))

(defn- use-backend? [{:keys [has-large-payload? results session-id]}]
  (and session-id
       (or has-large-payload?
           (and results (> (count results) client-side-threshold)))))

(defn- handle-client-download [{:keys [results matrix tabular?] :as props} format]
  (let [filename-meta (select-keys props [:connection-name :session-id])]
    (case format
      :txt (trigger-download! (build-filename filename-meta "txt") "text/plain" results)
      :csv (let [content (if (and tabular? matrix)
                           (matrix->csv matrix)
                           results)]
             (trigger-download! (build-filename filename-meta "csv") "text/csv" content))
      :json (let [heads (first matrix)
                  body (next matrix)
                  content (matrix->json heads body)]
              (trigger-download! (build-filename filename-meta "json") "application/json" content)))))

(defn- handle-download [{:keys [session-id] :as props} format]
  (if (use-backend? props)
    (rf/dispatch [:audit->session-file-generate session-id (name format)])
    (handle-client-download props format)))

(defn main
  "Dropdown menu offering output actions (view/copy/download).

   Required props:
     :results          Raw output string already rendered to the user.

   Optional props:
     :tabular?              When true, CSV/JSON entries are offered.
     :matrix                Parsed [[heads] [row] ...] used by CSV/JSON. Required when tabular?.
     :session-id            Used as filename hint and as the target for backend downloads.
     :connection-name       Used as filename hint.
     :has-large-payload?    When true, forces the backend download flow.
     :on-view-session-details Callback invoked when the user picks
                              \"View session details\". When provided, the
                              item is rendered; omit it on the session
                              details surfaces (where the action would be
                              a no-op).

   When the content exceeds the client-side threshold and a :session-id is
   available, downloads are delegated to the existing backend token flow."
  [{:keys [results tabular? on-view-session-details] :as props}]
  (let [download-disabled? @(rf/subscribe [:gateway->sessions-download-disabled?])
        clipboard-disabled? @(rf/subscribe [:gateway->clipboard-disabled?])
        has-content? (and results (not (cs/blank? results)))
        show-view? (boolean on-view-session-details)
        show-copy? (and has-content? (not clipboard-disabled?))
        show-downloads? (and has-content? (not download-disabled?))]
    (when (or show-view? show-copy? show-downloads?)
      [:> DropdownMenu.Root
       [:> DropdownMenu.Trigger
        [:> IconButton {:variant "ghost"
                        :color "gray"
                        :size "1"
                        :aria-label "Output options"}
         [:> MoreHorizontal {:size 16}]]]
       [:> DropdownMenu.Content {:align "end"}
        (when show-view?
          [:> DropdownMenu.Item {:class "cursor-pointer hover:bg-gray-2 text-gray-12"
                                 :on-select on-view-session-details}
           [:> Flex {:align "center" :gap "2"}
            [:> SquareArrowOutUpRight {:size 16}]
            [:> Text {:size "2"} "View session details"]]])
        (when show-copy?
          [:> DropdownMenu.Item {:class "cursor-pointer hover:bg-gray-2 text-gray-12"
                                 :on-select #(js/navigator.clipboard.writeText results)}
           [:> Flex {:align "center" :gap "2"}
            [:> Copy {:size 16}]
            [:> Text {:size "2"} "Copy logs content"]]])
        (when (and (or show-view? show-copy?) show-downloads?)
          [:> DropdownMenu.Separator])
        (when show-downloads?
          [:<>
           [:> DropdownMenu.Item {:class "cursor-pointer hover:bg-gray-2 text-gray-12"
                                  :on-select #(handle-download props :txt)}
            [:> Flex {:align "center" :gap "2"}
             [:> FileText {:size 16}]
             [:> Text {:size "2"} "Download as TXT"]]]
           (when tabular?
             [:<>
              [:> DropdownMenu.Item {:class "cursor-pointer hover:bg-gray-2 text-gray-12"
                                     :on-select #(handle-download props :csv)}
               [:> Flex {:align "center" :gap "2"}
                [:> Sheet {:size 16}]
                [:> Text {:size "2"} "Download as CSV"]]]
              [:> DropdownMenu.Item {:class "cursor-pointer hover:bg-gray-2 text-gray-12"
                                     :on-select #(handle-download props :json)}
               [:> Flex {:align "center" :gap "2"}
                [:> Braces {:size 16}]
                [:> Text {:size "2"} "Download as JSON"]]]])])]])))
