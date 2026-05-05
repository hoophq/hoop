(ns webapp.features.workflows.views.timeline
  (:require
   ["@radix-ui/themes" :refer [Box Callout Flex Text]]
   ["lucide-react" :refer [Info]]
   [re-frame.core :as rf]
   [webapp.features.workflows.views.step-card :as step-card]
   [webapp.formatters :as formatters]))

(defn- session->duration-ms [session]
  (let [start (formatters/iso->ms (:start_date session))
        end (formatters/iso->ms (:end_date session))]
    (when (and start end) (max 0 (- end start)))))

(defn- truncation-banner [total shown]
  [:> Callout.Root {:variant "soft" :color "amber" :size "1"}
   [:> Callout.Icon
    [:> Info {:size 14}]]
   [:> Callout.Text
    (str "Showing the first " shown " of " total " steps. Long workflows are "
         "capped to keep the timeline responsive.")]])

(defn- timeline-eyebrow
  "Small caps section heading sitting above the rail. Anchors the timeline
   block visually under the hero card."
  [step-count]
  [:> Flex {:align "center" :justify "between" :gap "3"
            :class "mb-5"}
   [:> Flex {:align "center" :gap "3"}
    [:> Text {:size "1" :weight "bold"
              :class "uppercase tracking-[0.18em] text-[--gray-11]"}
     "Timeline"]
    [:> Box {:class "h-px w-12 bg-[--gray-a5]"}]
    [:> Text {:size "2" :class "text-[--gray-11] tabular-nums"}
     (str step-count " " (if (= 1 step-count) "step" "steps"))]]
   [:> Text {:size "1" :class "text-[--gray-10]"}
    "ordered by start time"]])

(defn- step-row
  "One step row. Owns its own subscriptions so the parent timeline
   doesn't deref per-row state on every render."
  [session step-number last?]
  (let [session-id (:id session)
        status @(rf/subscribe [:workflows/step-status session])
        offset-ms @(rf/subscribe [:workflows/step-offset-ms session])
        expanded? @(rf/subscribe [:workflows/expanded? session-id])
        step-detail @(rf/subscribe [:workflows/step-detail session-id])]
    [step-card/step-card
     {:session session
      :step-number step-number
      :status status
      :offset-ms offset-ms
      :duration-ms (session->duration-ms session)
      :last? last?
      :expanded? expanded?
      :step-detail step-detail}]))

(defn timeline
  "Renders the vertical rail of step cards for the loaded workflow."
  []
  (let [sessions @(rf/subscribe [:workflows/sessions])
        truncated? @(rf/subscribe [:workflows/truncated?])
        total @(rf/subscribe [:workflows/total])
        last-idx (dec (count sessions))]
    [:> Box
     [timeline-eyebrow (count sessions)]
     [:> Flex {:direction "column" :gap "0"}
      (when truncated?
        [:> Box {:class "mb-4"}
         [truncation-banner total (count sessions)]])

      (doall
       (map-indexed
        (fn [idx session]
          ^{:key (:id session)}
          [step-row session (inc idx) (= idx last-idx)])
        sessions))]]))
