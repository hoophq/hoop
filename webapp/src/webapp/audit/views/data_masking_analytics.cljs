(ns webapp.audit.views.data-masking-analytics
  (:require
   ["@radix-ui/react-accordion" :as Accordion]
   ["@radix-ui/themes" :refer [Badge Box Button Flex Text Callout Link]]
   ["lucide-react" :refer [ChevronDown Sparkles LayoutList ArrowRightLeft ArrowUpRight]]
   [clojure.string :as cs]
   [re-frame.core :as rf]
   [reagent.core :as r]
   [webapp.config :as config]
   [webapp.http.api :as api]
   [webapp.utilities :as utilities]))

(def ^:private rdp-analysis-active-statuses
  "Statuses that should keep the polling loop running. Mirrors the playback
   view; once the job reaches a terminal state (done / failed) the loop stops."
  #{"pending" "running" "analyzing"})

(def ^:private rdp-analysis-poll-interval-ms 5000)

(def ^:private rdp-status-presentation
  "Maps the RDP PII analysis status returned by the gateway to its user-facing
   label and Radix Badge color. Mirrors the badge in session_data_rdp.cljs so
   the surfaces stay visually consistent."
  {"pending"      {:label "Queued"          :color "gray"}
   "running"      {:label "Analyzing PII"   :color "blue"}
   "analyzing"    {:label "Analyzing PII"   :color "blue"}
   "done"         {:label "Analysis ready"  :color "green"}
   "failed"       {:label "Analysis failed" :color "red"}
   "not_analyzed" {:label "Not analyzed"    :color "gray"}})

(defn- normalize-rdp-analysis [response]
  (let [info (:analysis response)
        status (or (:status info) (:analysis_status response) "")]
    {:status status
     :attempt (or (:attempt info) 0)
     :max-attempts (or (:max_attempts info) 0)
     :last-error (or (:last_error info) "")}))

(defn- rdp-analysis-status-badge
  "Compact RDP PII analysis status badge for the Live Data Masking title row.
   Returns nil when the status is empty (analysis disabled / non-RDP session).
   When :on-retry is provided and the analysis has failed a Retry button is
   rendered so the user can requeue the job without DB access."
  [{:keys [status attempt max-attempts last-error on-retry retrying?]}]
  (when (seq status)
    (let [{:keys [label color]} (get rdp-status-presentation status
                                     {:label (str "Status: " status) :color "gray"})]
      [:> Flex {:align "center" :gap "2" :wrap "wrap" :class "text-xs"}
       [:> Badge {:variant "soft" :color color :size "1"} label]
       (when (and (= status "running") (pos? attempt) (> max-attempts 1))
         [:> Badge {:variant "soft" :color "gray" :size "1"}
          (str "Attempt " attempt "/" max-attempts)])
       (when (and (= status "failed") (pos? attempt) (>= attempt max-attempts))
         [:> Badge {:variant "soft" :color "red" :size "1"}
          (str "Gave up after " max-attempts " attempts")])
       (when (and (= status "failed") (seq last-error))
         [:> Text {:size "1" :class "text-[--red-11]"} last-error])
       ;; Retry is offered when analysis can still be (re)started: a
       ;; permanently-failed job, or a never-run job (pre-activation
       ;; sessions surfaced as "not_analyzed").
       (when (and (or (= status "failed") (= status "not_analyzed"))
                  on-retry)
         [:> Button {:size "1"
                     :variant "soft"
                     :color "blue"
                     :disabled retrying?
                     :on-click (fn [e]
                                 (.stopPropagation e)
                                 (.preventDefault e)
                                 (on-retry))}
          (cond
            retrying?                    "Retrying…"
            (= status "not_analyzed")    "Run analysis"
            :else                        "Retry analysis")])])))

(defn no-presidio-callout []
  [:> Callout.Root {:size "2"
                    :class "w-full bg-[--violet-2] p-3"}
   [:> Callout.Icon
    [:> Sparkles {:size 16
                  :color "var(--violet-9)"}]]
   [:> Callout.Text {:class "text-gray-12"}
    [:> Text {:as "p" :size "2" :weight "bold" :class "mb-2"}
     "Unlock Live Data Masking"]
    [:> Text {:as "p" :size "2" :class "mb-2"}
     "Redact sensitive fields on the fly to reduce exposure risk and keep your data pipelines compliant."]
    [:> Flex {:direction "column" :gap "2"}
     [:> Link {:href "#"
               :class "text-primary-10 flex items-center gap-1 w-fit no-underline font-medium"
               :on-click (fn [e]
                           (.preventDefault e)
                           (rf/dispatch [:close-modal])
                           (rf/dispatch [:navigate :ai-data-masking]))}
      "Configure it on Live Data Masking"
      [:> ArrowUpRight {:size 16}]]
     [:> Link {:href (get-in config/docs-url [:features :ai-datamasking])
               :target "_blank"
               :class "text-primary-10 flex items-center gap-1 w-fit no-underline font-medium"}
      "Go to Live Data Masking Docs"
      [:> ArrowUpRight {:size 16}]]]]])

(defn- build-analytics-report [session-report session]
  (let [report-items (get-in session-report [:data :items])
        report-total (get-in session-report [:data :total_redact_count])
        report-ready? (= :ready (:status session-report))
        has-report-data? (and report-ready?
                              (or (seq report-items)
                                  (pos? (or report-total 0))))]
    (if has-report-data?
      session-report
      (let [data-analyzer (or (get-in session [:metrics :data_analyzer]) {})
            items (map (fn [[info-type count]]
                         {:info_type (name info-type)
                          :count count})
                       data-analyzer)
            total-redact (reduce + 0 (vals data-analyzer))]
        {:data {:items items
                :total_redact_count total-redact}}))))

(defn data-masking-analytics [session-report & {:keys [title subtitle hide-summary?]}]
  (let [redacted-types (map #(utilities/sanitize-string (:info_type %))
                            (-> session-report :data :items))
        total-redact (-> session-report :data :total_redact_count)
        total-items-text (str total-redact " " (if (<= total-redact 1) "item" "items"))
        count-less-1 (- (count redacted-types) 1)
        redacted-types-list (cs/join ", " redacted-types)
        redacted-types-display (if (pos? (count redacted-types))
                                 (str (count redacted-types)
                                      " (" redacted-types-list ")")
                                 "-")
        redacted-types-summary (if (pos? (count redacted-types))
                                 (let [first-type (first redacted-types)]
                                   (if (>= count-less-1 1)
                                     (str (count redacted-types) " (" first-type " + " count-less-1 " more)")
                                     (str (count redacted-types) " (" first-type ")")))
                                 "0")
        display-title (or title "Live Data Masking")]
    [:> (.-Root Accordion) {:type "single"
                            :collapsible true
                            :class "w-full p-3 bg-[--violet-2] rounded-md"}
     [:> (.-Item Accordion) {:value "ai-data-masking-analytics"
                             :className "border-none"}
      [:> (.-Header Accordion)
       [:> (.-Trigger Accordion) {:className "group flex w-full items-center justify-between gap-3 text-left text-base font-semibold text-sm focus:outline-none focus-visible:ring focus-visible:ring-gray-500 focus-visible:ring-opacity-75"}
        [:> Flex {:direction "column" :align "start" :gap "1" :class "min-w-0 flex-1"}
         [:> Flex {:align "center" :gap "2" :class "min-w-0"}
          [:> Sparkles {:size 16
                        :class "shrink-0"
                        :color "var(--violet-9)"}]
          [:> Text {:size "2" :weight "bold"} display-title]]
         (when subtitle
           (if (string? subtitle)
             [:> Text {:size "1" :class "text-gray-11"}
              subtitle]
             [:> Box {:class ""}
              subtitle]))]
       [:> Flex {:align "center" :gap "4" :class "flex-wrap justify-end text-xs"}
        (when-not hide-summary?
          [:> Box {:class "group-data-[state=open]:hidden"}
           [:> Text {:size "1"} "Data Categories: "]
           [:> Text {:size "1" :class "font-normal"}
            redacted-types-summary]
           [:> Text {:size "1"} " Volume of Data: "]
           [:> Text {:size "1" :class "font-normal"}
            total-items-text]])
         [:> ChevronDown {:size 16
                          :className "text-gray-10 transition-transform group-data-[state=open]:rotate-180 shrink-0"}]]]]
      [:> (.-Content Accordion) {:className "mt-3"}
       [:> Box {:class "grid grid-cols-2 gap-2 text-xs"}
        [:> Box {:class "flex flex-col justify-center items-center gap-1 rounded-md bg-[--violet-3] p-2"}
         [:> LayoutList {:size 16
                         :class "shrink-0"
                         :color "var(--violet-9)"}]
         [:> Text {:size "1"} "Data Categories"]
         [:> Text {:size "2" :class "font-semibold"}
          redacted-types-display]]
        [:> Box {:class "flex flex-col justify-center items-center gap-1 rounded-md bg-[--violet-3] p-2"}
         [:> ArrowRightLeft {:size 16
                             :class "shrink-0"
                             :color "var(--violet-9)"}]
         [:> Text {:size "1"} "Volume of Data"]
         [:> Text {:size "2" :class "font-semibold"}
          total-items-text]]]]]]))

(defn- rdp-session?
  "Returns true when the session is a RDP recording. The PII analysis pipeline
   is currently only wired for RDP, so we skip the extra fetch on every other
   subtype to avoid pointless 4xx round-trips on the audit endpoint."
  [session]
  (= "rdp" (:connection_subtype session)))

(defn- rdp-analysis-tracker
  "Reagent component that fetches the RDP PII analysis status for a session and
   polls every rdp-analysis-poll-interval-ms while the job is still running.
   Renders the compact rdp-analysis-status-badge inside the Live Data Masking
   title row so the user can see the status without opening the player. The
   poll loop is stopped automatically on a terminal status and on unmount.
   When the analysis is in a permanently-failed state, a Retry button is
   exposed via the badge so the user can requeue the job without DB access."
  [{:keys [session]}]
  (let [analysis (r/atom {:status ""})
        retrying? (r/atom false)
        poll-timer (r/atom nil)
        ;; Tracks the previous polled status so we can fire a one-shot side
        ;; effect (refresh the parent's session report) when the analysis
        ;; transitions into a terminal state. Without this the Live Data Masking
        ;; "Data Categories" / "Volume of Data" numbers stay stale until the
        ;; user closes and reopens the session details modal.
        prev-status (r/atom "")
        cancel-poll (fn []
                      (when-let [t @poll-timer]
                        (js/clearTimeout t)
                        (reset! poll-timer nil)))
        on-status-change
        (fn [new-status]
          (when (and (not= @prev-status new-status)
                     (= new-status "done"))
            ;; The worker writes the aggregate counts to two places that
            ;; feed different webapp surfaces:
            ;;   - sessions.metrics.data_analyzer (read by build-analytics-report
            ;;     as a fallback when the session-report is empty)
            ;;   - private.session_metrics       (read by /reports/sessions)
            ;; The session-report endpoint actually reads from a different
            ;; metrics path (metrics.data_masking.info_types) so the report
            ;; refresh alone won't surface the new numbers \u2014 we also need
            ;; the parent's :session map to be re-fetched so its embedded
            ;; metrics.data_analyzer JSON is current. Dispatching both keeps
            ;; the surfaces consistent regardless of which one ends up
            ;; populating analytics-report.
            (rf/dispatch [:reports->get-report-by-session-id session])
            (rf/dispatch [:audit->get-session-by-id {:id (:id session)
                                                     :verb (:verb session)}]))
          (reset! prev-status new-status))
        ;; Forward declarations so schedule, refresh and retry can refer
        ;; to each other.
        schedule-next (atom nil)
        refresh (fn refresh []
                  (api/request
                   {:method "GET"
                    :uri (str "/sessions/" (:id session) "/rdp-detections")
                    :on-success (fn [response]
                                  (let [next (normalize-rdp-analysis response)]
                                    (reset! analysis next)
                                    (on-status-change (:status next))
                                    (when (contains? rdp-analysis-active-statuses
                                                     (:status next))
                                      (@schedule-next))))
                    :on-failure (fn [err]
                                  (js/console.error "Failed to refresh RDP analysis status:" err)
                                  ;; Keep retrying on transient failures so a
                                  ;; flaky network doesn't leave the badge
                                  ;; stuck mid-analysis.
                                  (@schedule-next))}))
        request-retry (fn []
                        (when-not @retrying?
                          (reset! retrying? true)
                          (api/request
                           {:method "POST"
                            :uri (str "/sessions/" (:id session)
                                      "/rdp-detections/retry")
                            :on-success (fn [response]
                                          (reset! retrying? false)
                                          (let [next (normalize-rdp-analysis response)]
                                            (reset! analysis next)
                                            (on-status-change (:status next))
                                            (when (contains? rdp-analysis-active-statuses
                                                             (:status next))
                                              (@schedule-next))))
                            :on-failure (fn [err]
                                          (js/console.error "Failed to retry RDP analysis:" err)
                                          (reset! retrying? false))})))]
    (reset! schedule-next
            (fn []
              (cancel-poll)
              (reset! poll-timer
                      (js/setTimeout refresh rdp-analysis-poll-interval-ms))))
    (r/create-class
     {:display-name "rdp-analysis-tracker"
      :component-did-mount (fn [] (refresh))
      :component-will-unmount (fn [] (cancel-poll))
      :reagent-render
      (fn [_]
        [rdp-analysis-status-badge
         (assoc @analysis
                :on-retry request-retry
                :retrying? @retrying?)])})))

(defn main [{:keys [session]}]
  (let [user (rf/subscribe [:users->current-user])
        gateway-info (rf/subscribe [:gateway->info])
        session-report (rf/subscribe [:reports->session])
        free-license? (-> @user :data :free-license?)
        redact-provider (-> @gateway-info :data :redact_provider)
        analytics-report (build-analytics-report @session-report session)
        ;; The status badge sits in the title row's :subtitle slot so it's
        ;; visible whether the accordion is collapsed or expanded.
        rdp-status-subtitle (when (rdp-session? session)
                              [rdp-analysis-tracker {:session session}])]
    (cond
      (not= redact-provider "mspresidio")
      [no-presidio-callout]

      free-license?
      [data-masking-analytics
       analytics-report
       {:title "Enable Live Data Masking"
        :hide-summary? true
        :subtitle [:> Text {:size "2" :class "font-normal"}
                   "We detected sensitive data that could protected with automated data masking. "
                   [:> Link {:href "#"
                             :class "text-primary-10 inline-flex items-center no-underline font-medium"
                             :on-click (fn [e]
                                         (.preventDefault e)
                                         (rf/dispatch [:close-modal])
                                         (rf/dispatch [:navigate :ai-data-masking]))}
                    "Configure it on Live Data Masking"
                    [:> ArrowUpRight {:size 14}]]]}]

      :else
      (if rdp-status-subtitle
        [data-masking-analytics analytics-report :subtitle rdp-status-subtitle]
        [data-masking-analytics analytics-report]))))