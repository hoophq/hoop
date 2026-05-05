(ns webapp.features.workflows.views.step-card
  (:require
   ["@radix-ui/themes" :refer [Badge Box Button Code Flex ScrollArea Text]]
   ["lucide-react" :refer [ArrowUpRight ChevronDown CircleCheckBig
                           Clock2 OctagonX ShieldAlert]]
   [clojure.string :as string]
   [re-frame.core :as rf]
   [webapp.audit.views.session-details :as session-details]
   [webapp.connections.constants :as connection-constants]
   [webapp.formatters :as formatters]))

;; ─── Status helpers ─────────────────────────────────────────────────────────

(defn- status-color
  "CSS color value used by the rail and accent strip for a given status."
  [status]
  (case status
    :running "var(--yellow-9)"
    :error   "var(--red-9)"
    :success "var(--green-9)"
    "var(--gray-7)"))

(defn- status-soft-color
  "Faint tint used as the expanded card's left accent strip."
  [status]
  (case status
    :running "var(--yellow-a5)"
    :error   "var(--red-a5)"
    :success "var(--green-a5)"
    "var(--gray-a5)"))

;; ─── Rail node ──────────────────────────────────────────────────────────────

(defn- status-node
  "The 32px node sitting on the timeline rail. Carries an icon for the
   running/error/success states; falls back to the step number for any
   neutral state. The white-page ring isolates it from the rail line."
  [status step-number]
  (let [color (status-color status)
        [icon icon-color] (case status
                            :running [Clock2          "text-white"]
                            :error   [OctagonX        "text-white"]
                            :success [CircleCheckBig  "text-white"]
                            [nil "text-white"])]
    [:> Box {:class "relative z-10"}
     ;; outer halo for running/error so the eye lands on it
     (when (or (= status :running) (= status :error))
       [:> Box {:class (str "absolute inset-0 rounded-full blur-md opacity-50 "
                            (when (= status :running) "animate-pulse"))
                :style {:backgroundColor color}}])
     [:> Box {:class (str "relative flex items-center justify-center "
                          "w-8 h-8 rounded-full text-white "
                          "ring-4 ring-[--color-page-background] "
                          (when (= status :running) "animate-pulse"))
              :style {:backgroundColor color
                      :boxShadow "0 1px 2px rgba(0,0,0,0.12), inset 0 1px 0 rgba(255,255,255,0.18)"}}
      (if icon
        [:> icon {:size 14 :class icon-color}]
        [:> Text {:size "1" :weight "bold" :class "tabular-nums text-white"}
         step-number])]]))

(defn- status-badge [status]
  (let [[color label] (case status
                        :running [:yellow "Running"]
                        :error   [:red "Failed"]
                        :success [:green "Succeeded"]
                        [:gray "Unknown"])]
    [:> Badge {:color (name color) :variant "soft" :size "1"
               :class (when (= status :running) "animate-pulse")}
     label]))

;; ─── Card sub-components ────────────────────────────────────────────────────

(defn- connection-icon [session]
  (let [conn-shape {:subtype (:connection_subtype session)
                    :type (:type session)}
        src (connection-constants/get-connection-icon conn-shape "default")]
    [:figure {:class (str "flex items-center justify-center "
                          "w-10 h-10 rounded-3 "
                          "bg-gradient-to-b from-[--gray-1] to-[--gray-3] "
                          "border border-[--gray-a4] "
                          "shrink-0 overflow-hidden")}
     [:img {:src src :class "w-5 h-5"}]]))

(defn- script-preview
  "One-line truncated preview of the script for the collapsed state."
  [session]
  (let [raw (or (-> session :script :data) "")
        single-line (-> raw
                        (string/replace #"\s+" " ")
                        string/trim)]
    (when-not (string/blank? single-line)
      [:> Text {:size "2"
                :class (str "font-mono text-[--gray-11] truncate "
                            "min-w-0 grow")}
       single-line])))

(defn- chip
  "Tiny inline metadata pill — used for duration, etc."
  [icon-component label]
  [:> Flex {:align "center" :gap "1"
            :class (str "px-2 py-0.5 rounded-2 "
                        "bg-[--gray-2] border border-[--gray-a3] "
                        "text-[--gray-11]")}
   [:> icon-component {:size 12}]
   [:> Text {:size "1" :weight "medium" :class "tabular-nums"}
    label]])

(defn- offset-tag
  "Time-since-start tag. Subdued by default; gains a subtle accent so it
   reads as the rail's continuous timeline coordinate."
  [offset-ms]
  [:> Text {:size "1"
            :class (str "font-mono text-[--gray-11] tabular-nums "
                        "whitespace-nowrap px-1.5 py-0.5 rounded-1 "
                        "bg-[--gray-a3]")}
   (formatters/time-offset->compact offset-ms)])

(defn- code-block [content]
  (if (string/blank? content)
    [:> Text {:size "1" :class "italic text-[--gray-10]"}
     "No content"]
    [:> ScrollArea {:type "auto" :scrollbars "both" :size "1"
                    :style {:maxHeight "240px"}}
     [:> Box {:class (str "p-3 rounded-3 "
                          "bg-[--gray-12] border border-[--gray-a4] "
                          "font-mono text-xs text-[--gray-1] "
                          "whitespace-pre-wrap break-words "
                          "shadow-[inset_0_1px_0_rgba(255,255,255,0.04)]")}
      content]]))

(defn- detail-row [label content]
  [:> Flex {:direction "column" :gap "1"}
   [:> Text {:size "1" :weight "bold"
             :class "uppercase tracking-[0.14em] text-[--gray-11]"}
    label]
   content])

;; ─── Expanded panel ─────────────────────────────────────────────────────────

(defn- expanded-content [session step-detail]
  (let [status (:status step-detail)
        full (or (:data step-detail) session)
        script (or (-> full :script :data) "")
        guardrails (or (:guardrails_info full) [])
        guardrails-count (count guardrails)
        exit-code (:exit_code full)
        machine? (= "machine" (:identity_type full))]
    [:> Box {:class (str "px-5 py-5 space-y-radix-4 "
                         "border-t border-[--gray-a3] "
                         "bg-gradient-to-b from-[--gray-1] to-white")}
     (cond
       (= status :loading)
       [:> Flex {:align "center" :gap "2"}
        [:> Text {:size "2" :class "italic text-[--gray-11]"}
         "Loading details…"]]

       (= status :error)
       [:> Text {:size "2" :class "text-[--red-11]"}
        "Could not load details for this step."]

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

;; ─── Step card (public) ─────────────────────────────────────────────────────

(defn step-card
  "One row of the timeline. Receives:
   - session: the session map
   - step-number: 1-indexed integer
   - status: :running | :error | :success
   - offset-ms: milliseconds since workflow start
   - duration-ms: total duration of this session in ms (or nil)
   - last? : true if this is the last step (omit the trailing rail line)
   - expanded?: boolean
   - step-detail: result of :workflows/step-detail subscription"
  [{:keys [session step-number status offset-ms duration-ms
           last? expanded? step-detail]}]
  (let [accent (status-color status)
        soft   (status-soft-color status)]
    [:> Flex {:gap "4" :align "stretch"}
     ;; ─── Rail ─────────────────────────────────────────────────────
     [:> Box {:class "relative flex flex-col items-center w-8 shrink-0"}
      [status-node status step-number]
      (when-not last?
        ;; Colored rail segment leading down to the next node. The line
        ;; takes the *current* step's color so the rail visually narrates
        ;; what just happened on the way down.
        [:> Box {:class "absolute top-8 left-1/2 -translate-x-1/2 w-px grow h-[calc(100%-2rem)]"
                 :style {:background
                         (str "linear-gradient(to bottom, "
                              accent " 0%, "
                              "color-mix(in srgb, " accent " 30%, transparent) 60%, "
                              "var(--gray-a4) 100%)")}}])]

     ;; ─── Card ────────────────────────────────────────────────────
     [:> Box {:class "grow min-w-0 pb-5"}
      [:> Box {:on-click #(rf/dispatch [:workflows/toggle-step session])
               :class (str "group cursor-pointer rounded-4 border bg-white "
                           "transition-all duration-200 "
                           "relative overflow-hidden "
                           (if expanded?
                             "border-[--gray-a5] shadow-[0_8px_30px_-12px_rgba(0,0,0,0.12)]"
                             (str "border-[--gray-a4] "
                                  "hover:border-[--gray-a6] "
                                  "hover:shadow-[0_4px_16px_-8px_rgba(0,0,0,0.08)] "
                                  "hover:-translate-y-px")))}

       ;; Left accent strip — visible only when expanded, colored by status
       (when expanded?
         [:> Box {:class "absolute left-0 top-0 bottom-0 w-1"
                  :style {:background
                          (str "linear-gradient(to bottom, "
                               accent ", "
                               "color-mix(in srgb, " accent " 60%, transparent))")}}])

       ;; Header row
       [:> Flex {:align "center" :gap "3"
                 :class "px-4 py-3.5"}
        [connection-icon session]

        [:> Flex {:direction "column" :class "min-w-0 grow" :gap "1"}
         [:> Flex {:align "center" :gap "2" :class "min-w-0"}
          [:> Text {:size "1" :weight "bold"
                    :class (str "uppercase tracking-[0.14em] "
                                "text-[--gray-11] tabular-nums")}
           (str "Step " step-number)]
          [:> Box {:class "h-3 w-px bg-[--gray-a5]"}]
          [offset-tag offset-ms]
          [:> Text {:size "3" :weight "bold"
                    :class "text-[--gray-12] truncate"}
           (or (:connection session) (:role_name session) "—")]
          (when (:type session)
            [:> Badge {:color "gray" :variant "soft" :size "1"}
             (:type session)])]

         (when-not expanded?
           [script-preview session])]

        [:> Flex {:align "center" :gap "2" :class "shrink-0"}
         [chip Clock2 (formatters/duration-ms->compact duration-ms)]
         [status-badge status]
         [:> Box {:class (str "flex items-center justify-center "
                              "w-7 h-7 rounded-full text-[--gray-11] "
                              "transition-all duration-200 "
                              (if expanded?
                                "bg-[--gray-a4] rotate-180"
                                "group-hover:bg-[--gray-a3]"))}
          [:> ChevronDown {:size 16}]]]]

       ;; Expanded section
       [:> Box {:class (str "overflow-hidden transition-all duration-300 ease-in-out "
                            (if expanded?
                              "max-h-[1500px] opacity-100"
                              "max-h-0 opacity-0"))}
        (when expanded?
          [expanded-content session step-detail])]]]]))
