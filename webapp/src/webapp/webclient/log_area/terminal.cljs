(ns webapp.webclient.log-area.terminal
  (:require ["@heroicons/react/20/solid" :as hero-solid-icon]
            ["@heroicons/react/24/outline" :as hero-outline-icon]
            ["clipboard" :as clipboardjs]
            ["highlight.js" :as hljs]
            ["markdown-it" :as markdownit]
            [clojure.string :as cs]
            [goog.crypt.base64 :as b64]
            [re-frame.core :as rf]
            [reagent.dom.server :as rdom]
            [webapp.audit.views.session-details :as session-details]
            [webapp.formatters :as formatters]))

(defn trunc
  [string]
  (let [string-splited (cs/split string #"\n")
        take-5 (take 5 string-splited)
        join-5 (cs/join "\n" take-5)]
    (subs join-5 0 (min (count string) 4000))))

(defn copy-clipboard [data-clipboard-target]
  [:div {:class (str "copy-to-clipboard absolute rounded-lg p-x-small "
                     "top-2 right-2 cursor-pointer box-border "
                     "opacity-0 group-hover:opacity-100 transition z-20")
         :data-clipboard-target data-clipboard-target}
   [:> hero-solid-icon/ClipboardDocumentIcon {:class "h-6 w-6 shrink-0 text-white"
                                              :aria-hidden "true"}]])

(defn action-buttons-container [session-id]
  [:div {:class "absolute top-2 right-2"}
   [:div {:class (str "rounded-lg p-x-small "
                      "cursor-pointer box-border hover:bg-gray-50 hover:bg-opacity-20 "
                      "opacity-0 group-hover:opacity-100 transition z-20")
          :on-click #(rf/dispatch [:open-modal
                                   [session-details/main {:id session-id :verb "exec"}]
                                   :large
                                   (fn []
                                     (rf/dispatch [:audit->clear-session])
                                     (rf/dispatch [:close-modal]))])}
    [:> hero-outline-icon/ArrowTopRightOnSquareIcon {:class "h-6 w-6 shrink-0 text-white"
                                                     :aria-hidden "true"}]]])

(defn- ai-response-area-list
  [status {:keys [response script execution-time]}]
  (let [_ (new clipboardjs ".copy-to-clipboard")
        md (markdownit #js{:html true
                           :highlight (fn [string lang]
                                        (let [container-id (b64/encodeString string 4)]
                                          (if (and lang (hljs/getLanguage lang))
                                            (try
                                              (str "<pre class=\"relative group\" id=\""
                                                   container-id
                                                   "\">"
                                                   (rdom/render-to-static-markup (copy-clipboard (str "#" container-id)))
                                                   "<code class=\"hljs\">"
                                                   (.-value (hljs/highlight string #js{:language lang}))
                                                   "</code></pre>")
                                              (catch js/Error _ (str "")))
                                            "")))})]
    (case status
      :success [:div {:class "relative py-large px-regular border-t border-gray-700 whitespace-pre-wrap"}
                [:div {:class "font-bold text-sm mb-1"}
                 script]
                [:div {:class "text-sm mb-1"
                       :dangerouslySetInnerHTML {:__html (.render md response)}}]
                [:div {:class "text-gray-400 text-sm"}
                 (str (formatters/current-time) " [cost " (formatters/time-elapsed execution-time) "]")]]
      :loading [:div {:class "flex gap-regular py-large px-regular border-t border-gray-700"}
                [:span "loading"]
                [:figure {:class "w-4"}
                 [:img {:class "animate-spin"
                        :src "/icons/icon-loader-circle-white.svg"}]]]
      :failure [:div {:class " group relative py-large px-regular border-t border-gray-700 whitespace-pre-wrap"}
                [:div {:class "font-bold text-sm mb-1"}
                 script]
                [:div {:class "text-sm mb-1"}
                 "There was an error to get the logs for this task"]
                [:div {:class "text-red-400 text-sm"}
                 (str (formatters/current-time) " [cost " (formatters/time-elapsed execution-time) "]")]]
      "No response to show")))

(defn- logs-area-list
  [status {:keys [logs logs-status script execution-time has-review? session-id]}]
  (case status
    :success (if has-review?
               [:div {:class "group relative py-large px-regular border-t border-gray-700 whitespace-pre-wrap"
                      :on-click (fn []
                                  (rf/dispatch [:open-modal
                                                [session-details/main {:id session-id :verb "exec"}]
                                                :large
                                                (fn []
                                                  (rf/dispatch [:audit->clear-session])
                                                  (rf/dispatch [:close-modal]))]))}
                [action-buttons-container session-id]
                [:div {:class "font-bold text-sm mb-1"}
                 script]
                [:div {:class "text-sm mb-1"}
                 "This task need to be reviewed. Please click here to see the details."]
                [:div {:class "text-gray-400 text-sm"}
                 (str (formatters/current-time) " [cost " (formatters/time-elapsed execution-time) "]")]]

               [:div {:class " group relative py-large px-regular border-t border-gray-700 whitespace-pre-wrap"}
                [action-buttons-container session-id]
                [:div {:class "font-bold text-sm mb-1"}
                 script]
                [:div {:class "text-sm mb-1"}
                 (trunc logs)]
                [:div {:class (str (if (= logs-status "success")
                                     "text-gray-400 text-sm"
                                     "text-gray-400 text-sm"))}
                 (str (formatters/current-time) " [cost " (formatters/time-elapsed execution-time) "]")]])
    :loading [:div {:class "flex gap-regular py-large px-regular border-t border-gray-700"}
              [:span "loading"]
              [:figure {:class "w-4"}
               [:img {:class "animate-spin"
                      :src "/icons/icon-loader-circle-white.svg"}]]]
    :failure [:div {:class " group relative py-large px-regular border-t border-gray-700 whitespace-pre-wrap"}
              [action-buttons-container session-id]
              [:div {:class "font-bold text-sm mb-1"}
               script]
              [:div {:class "text-sm mb-1"}
               "There was an error to get the logs for this task"]
              [:div {:class "text-gray-400 text-sm"}
               (str (formatters/current-time) " [cost " (formatters/time-elapsed execution-time) "]")]]
    "No logs to show"))

(defn main
  "config is a map with the following fields:
  :status -> possible values are :success :loading :failure. Anything different will be default to an generic error message
  :id -> id to differentiate more than one log on the same page.
  :logs -> the actual string with the logs"
  [type config]
  [:section
   {:class (str "relative bg-gray-900 font-mono h-full"
                " whitespace-pre text-gray-200 text-sm overflow-y-auto"
                " pt-regular h-full flex flex-col-reverse pb-12")
    :style {:overflow-anchor "none"}}
   (for [data config]
     (case type
       :terminal
       ^{:key (str (:status data) "-" (:response-id data))}
       [logs-area-list (:status data)
        {:logs (:response data)
         :logs-status (:response-status data)
         :script (:script data)
         :execution-time (:execution-time data)
         :has-review? (:has-review data)
         :session-id (:response-id data)}]

       :ai
       ^{:key (str (:status data) "-" (:response-id data))}
       [ai-response-area-list (:status data)
        {:response (:response data)
         :script (:script data)
         :execution-time (:execution-time data)}]))])
