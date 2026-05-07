(ns webapp.features.workflows.views.session-card
  (:require
   ["@radix-ui/react-accordion" :as Accordion]
   ["@radix-ui/themes" :refer [Badge Box Button Code Flex ScrollArea Text]]
   ["lucide-react" :refer [ArrowUpRight ChevronDown CircleCheckBig
                           Clock2 OctagonX ShieldAlert]]
   [clojure.string :as string]
   [re-frame.core :as rf]
   [webapp.audit.views.session-details :as session-details]
   [webapp.connections.constants :as connection-constants]
   [webapp.formatters :as formatters]))

;; ─── Status helpers ─────────────────────────────────────────────────────────

(defn- status-badge [status]
  (let [[color label icon] (case status
                             :running [:yellow "Running" Clock2]
                             :error   [:red "Failed" OctagonX]
                             :success [:green "Succeeded" CircleCheckBig]
                             [:gray "Unknown" nil])]
    [:> Badge {:color (name color) :variant "soft" :size "1"
               :class (when (= status :running) "animate-pulse")}
     [:> Flex {:align "center" :gap "1"}
      (when icon [:> icon {:size 12}])
      label]]))

;; ─── Header sub-components ──────────────────────────────────────────────────

(defn- connection-icon [session]
  (let [conn-shape {:subtype (:connection_subtype session)
                    :type (:type session)}
        src (connection-constants/get-connection-icon conn-shape "default")]
    [:figure {:class (str "flex items-center justify-center "
                          "w-9 h-9 rounded-3 "
                          "bg-[--gray-2] border border-[--gray-a4] "
                          "shrink-0 overflow-hidden")}
     [:img {:src src :class "w-5 h-5"}]]))

;; ─── Expanded content ───────────────────────────────────────────────────────

(defn- code-block [content]
  (if (string/blank? content)
    [:> Text {:size "1" :class "italic text-[--gray-10]"}
     "No content"]
    [:> ScrollArea {:type "auto" :scrollbars "both" :size "1"
                    :style {:maxHeight "240px"}}
     [:> Box {:class (str "p-3 rounded-3 "
                          "bg-[--gray-12] border border-[--gray-a4] "
                          "font-mono text-xs text-[--gray-1] "
                          "whitespace-pre-wrap break-words")}
      content]]))

(defn- detail-row [label content]
  [:> Flex {:direction "column" :gap "1"}
   [:> Text {:size "1" :weight "bold"
             :class "uppercase tracking-wider text-[--gray-11]"}
    label]
   content])

(defn- expanded-content [session step-detail]
  (let [status (:status step-detail)
        full (or (:data step-detail) session)
        script (or (-> full :script :data) "")
        guardrails (or (:guardrails_info full) [])
        guardrails-count (count guardrails)
        exit-code (:exit_code full)
        machine? (= "machine" (:identity_type full))]
    [:> Box {:class "px-radix-5 py-radix-4 space-y-radix-4 bg-white"}
     (cond
       ;; nil before the first :workflows/get-step-detail dispatch processes
       (or (nil? step-detail) (= status :loading))
       [:> Flex {:align "center" :gap "2"}
        [:> Text {:size "2" :class "italic text-[--gray-11]"}
         "Loading details…"]]

       (= status :error)
       [:> Text {:size "2" :class "text-[--red-11]"}
        "Could not load details for this session."]

       :else
       [:<>
        [detail-row "Script"
         [code-block script]]

        [:> Flex {:gap "5" :wrap "wrap"}
         [detail-row "Connection"
          [:> Text {:size "2" :weight "medium" :class "text-[--gray-12]"}
           (or (:connection full) (:role_name full))]]

         (when (:connection_subtype full)
           [detail-row "Type"
            [:> Text {:size "2" :class "text-[--gray-12]"}
             (:connection_subtype full)]])

         (when (some? exit-code)
           [detail-row "Exit code"
            [:> Code {:size "1"
                      :variant "soft"
                      :color (if (zero? exit-code) "green" "red")}
             (str exit-code)]])

         [detail-row "Identity"
          [:> Flex {:align "center" :gap "2"}
           [:> Text {:size "2" :class "text-[--gray-12]"}
            (or (:user_name full) (:user full) "—")]
           (when machine?
             [:> Badge {:color "gray" :variant "soft" :size "1"}
              "machine"])]]

         (when (pos? guardrails-count)
           [detail-row "Guardrails"
            [:> Flex {:align "center" :gap "1" :class "text-[--orange-11]"}
             [:> ShieldAlert {:size 14}]
             [:> Text {:size "2" :weight "medium"}
              (str guardrails-count " "
                   (if (= 1 guardrails-count) "hit" "hits"))]]])]

        [:> Flex {:justify "end" :class "pt-2"}
         [:> Button {:size "2"
                     :variant "soft"
                     :color "gray"
                     :highContrast true
                     :on-click (fn []
                                 (rf/dispatch [:modal->open
                                               {:id "session-details"
                                                :maxWidth "95vw"
                                                :content [session-details/main full]}]))}
          [:> ArrowUpRight {:size 14}]
          [:> Text {:size "2" :weight "medium"} "Open full session"]]]])]))

;; ─── Accordion item (public) ────────────────────────────────────────────────

(defn session-item
  "Renders a single accordion item for a session in the workflow.

   Receives:
   - session: the session map (used as accordion value)
   - position: 1-indexed integer
   - status: :running | :error | :success
   - duration-ms: total duration of this session in ms (or nil)
   - step-detail: result of :workflows/step-detail subscription"
  [{:keys [session position status duration-ms step-detail]}]
  [:> (.-Item Accordion) {:value (:id session)
                          :className (str "border border-[--gray-a4] bg-white "
                                          "first:rounded-t-4 last:rounded-b-4 "
                                          "[&:not(:first-child)]:border-t-0 "
                                          "data-[state=open]:bg-[--gray-1]")}
   [:> (.-Header Accordion)
    [:> (.-Trigger Accordion)
     {:className (str "group flex w-full items-center gap-3 px-radix-4 py-radix-3 "
                      "text-left focus:outline-none "
                      "hover:bg-[--gray-2] data-[state=open]:hover:bg-[--gray-1] "
                      "first:rounded-t-4 last:rounded-b-4 "
                      "data-[state=open]:rounded-b-none")}
     [connection-icon session]

     [:> Flex {:direction "column" :class "min-w-0 grow" :gap "1"}
      [:> Flex {:align "center" :gap "2" :class "min-w-0"}
       [:> Text {:size "1" :weight "bold"
                 :class "uppercase tracking-wider text-[--gray-11] tabular-nums"}
        (str "#" position)]
       [:> Box {:class "h-3 w-px bg-[--gray-a5]"}]
       [:> Text {:size "3" :weight "bold"
                 :class "text-[--gray-12] truncate"}
        (or (:connection session) (:role_name session) "—")]
       (when (:type session)
         [:> Badge {:color "gray" :variant "soft" :size "1"}
          (:type session)])]]

     [:> Flex {:align "center" :gap "2" :class "shrink-0"}
      [:> Flex {:align "center" :gap "1"
                :class (str "px-2 py-0.5 rounded-2 "
                            "bg-[--gray-2] border border-[--gray-a3] "
                            "text-[--gray-11]")}
       [:> Clock2 {:size 12}]
       [:> Text {:size "1" :weight "medium" :class "tabular-nums"}
        (formatters/duration-ms->compact duration-ms)]]
      [status-badge status]
      [:> ChevronDown {:size 16
                       :className (str "text-[--gray-11] transition-transform "
                                       "duration-200 group-data-[state=open]:rotate-180")}]]]]

   [:> (.-Content Accordion) {:className "border-t border-[--gray-a4]"}
    [expanded-content session step-detail]]])
