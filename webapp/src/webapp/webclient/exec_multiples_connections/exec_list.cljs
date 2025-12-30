(ns webapp.webclient.exec-multiples-connections.exec-list
  (:require ["@radix-ui/themes" :refer [Badge Box Button Card Flex Heading Text]]
            ["lucide-react" :refer [Check Loader2 AlertTriangle Clock Play ExternalLink]]
            [clojure.string :as cs]
            [re-frame.core :as rf]))

(defn ready-bar []
  [:> Badge {:variant "soft" :color "gray"}
   [:> Flex {:align "center" :gap "1"}
    [:> Check {:size 14}]
    [:> Text {:size "1"} "Ready"]]])

(defn running-bar []
  [:> Badge {:variant "soft" :color "blue"}
   [:> Flex {:align "center" :gap "1"}
    [:> Loader2 {:size 14 :class "animate-spin"}]
    [:> Text {:size "1"} "Running"]]])

(defn completed-bar []
  [:> Badge {:variant "soft" :color "green"}
   [:> Flex {:align "center" :gap "1"}
    [:> Check {:size 14}]
    [:> Text {:size "1"} "Completed"]]])

(defn error-bar []
  [:> Badge {:variant "soft" :color "red"}
   [:> Flex {:align "center" :gap "1"}
    [:> AlertTriangle {:size 14}]
    [:> Text {:size "1"} "Error"]]])

(defn waiting-review-bar []
  [:> Badge {:variant "soft" :color "yellow"}
   [:> Flex {:align "center" :gap "1"}
    [:> Clock {:size 14}]
    [:> Text {:size "1"} "Waiting Review"]]])

(defn button-group-running []
  [:> Flex {:justify "end" :align "center" :gap "3" :mt "6"}
   [:> Text {:size "2" :color "gray"}
    "Keep this screen open while your command is running"]
   [:> Button {:disabled true}
    [:> Flex {:align "center" :gap "2"}
     [:> Play {:size 16}]
     [:> Text "Run"]]]])

(defn button-group-ready [exec-list]
  [:> Flex {:justify "between" :align "center" :gap "3" :mt "6"}
   [:> Button {:variant "soft"
               :color "gray"
               :size "3"
               :on-click #(rf/dispatch [:parallel-mode/clear-execution])}
    "Close"]
   [:> Button {:size "3"
               :on-click #(rf/dispatch [:parallel-mode/execute-script exec-list])}
    [:> Flex {:align "center" :gap "2"}
     [:> Play {:size 16}]
     [:> Text "Run"]]]])

(defn button-group-completed [exec-list]
  (rf/dispatch [:editor-plugin->multiple-connections-update-metadata exec-list])
  [:> Flex {:justify "between" :align "center" :gap "3" :mt "6"}
   [:> Button {:variant "soft"
               :color "gray"
               :size "3"
               :on-click #(rf/dispatch [:parallel-mode/clear-execution])}
    "Close"]
   [:a {:href (str (. (. js/window -location) -origin)
                   "/sessions/filtered?id="
                   (cs/join "," (map :session-id exec-list)))
        :target "_blank"
        :rel "noopener noreferrer"}
    [:> Button {:size "3"}
     [:> Flex {:align "center" :gap "2"}
      [:> ExternalLink {:size 16}]
      [:> Text "Open in new tab"]]]]])

(defn main []
  (let [multi-exec (rf/subscribe [:multiple-connection-execution/modal])]
    (rf/dispatch [:editor-plugin->clear-connection-script])
    (fn []
      [:div {:id "modal"
             :class "fixed z-50 inset-0 overflow-y-auto"
             "aria-modal" true}
       [:div {"aria-hidden" "true"
              :class "fixed w-full h-full inset-0 bg-black bg-opacity-80 transition"}]
       [:> Box {:class (str "relative mb-large m-auto "
                            "mx-auto mt-16 lg:mt-large overflow-auto "
                            "w-full max-w-xs lg:max-w-4xl")
                :style {:backgroundColor "var(--gray-1)"
                        :border "1px solid var(--gray-4)"
                        :borderRadius "var(--radius-3)"
                        :boxShadow "var(--shadow-5)"}}
        [:> Box {:p "6"}
         [:> Heading {:size "4" :weight "bold" :mb "4"} "Review and Run"]
         [:> Card {:variant "classic"}
          (doall
           (for [exec (:data @multi-exec)]
             ^{:key (:connection-name exec)}
             [:> Box {:p "3" :class "border-b border-gray-3 last:border-b-0"}
              [:> Flex {:justify "between" :align "center" :gap "4"}
               [:> Flex {:direction "column"}
                [:> Text {:size "2" :weight "bold" :mb "1"}
                 (:connection-name exec)]
                [:> Text {:size "1" :color "gray"}
                 (:subtype exec)]]

               [:> Flex {:align "center" :gap "6"}
                (when (:session-id exec)
                  [:> Flex {:align "center" :gap "2"}
                   [:> Text {:size "1" :color "gray"} "ID:"]
                   [:> Text {:size "1" :weight "medium"}
                    (first (cs/split (:session-id exec) #"-"))]
                   [:a {:href (str (. (. js/window -location) -origin) "/sessions/" (:session-id exec))
                        :target "_blank"
                        :rel "noopener noreferrer"}
                    [:> ExternalLink {:size 16 :class "text-gray-700 hover:text-gray-900"}]]])

                (case (:status exec)
                  :ready [ready-bar]
                  :running [running-bar]
                  :completed [completed-bar]
                  :error [error-bar]
                  :waiting-review [waiting-review-bar])]]]))]
         (case (:status @multi-exec)
           :ready [button-group-ready (:data @multi-exec)]
           :running [button-group-running]
           :completed [button-group-completed (:data @multi-exec)])]]])))
