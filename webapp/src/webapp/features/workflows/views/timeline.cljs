(ns webapp.features.workflows.views.timeline
  (:require
   ["@radix-ui/react-accordion" :as Accordion]
   ["@radix-ui/themes" :refer [Box Callout Flex Text]]
   ["lucide-react" :refer [Info]]
   [re-frame.core :as rf]
   [webapp.features.workflows.views.session-card :as session-card]
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
    (str "Showing the first " shown " of " total " sessions. Long workflows are "
         "capped to keep the timeline responsive.")]])

(defn- list-eyebrow
  "Small caps section heading sitting above the accordion list."
  [sessions-count]
  [:> Flex {:align "center" :justify "between" :gap "3"
            :class "mb-radix-3"}
   [:> Flex {:align "center" :gap "3"}
    [:> Text {:size "1" :weight "bold"
              :class "uppercase tracking-wider text-[--gray-11]"}
     "Sessions"]
    [:> Box {:class "h-px w-12 bg-[--gray-a5]"}]
    [:> Text {:size "2" :class "text-[--gray-11] tabular-nums"}
     (str sessions-count " " (if (= 1 sessions-count) "session" "sessions"))]]
   [:> Text {:size "1" :class "text-[--gray-10]"}
    "ordered by start time"]])

(defn- session-row
  "One accordion item. Owns its own subscriptions so the parent timeline
   doesn't deref per-row state on every render."
  [session]
  (let [session-id (:id session)
        status @(rf/subscribe [:workflows/session-status session])
        step-detail @(rf/subscribe [:workflows/step-detail session-id])]
    [session-card/session-item
     {:session session
      :status status
      :duration-ms (session->duration-ms session)
      :step-detail step-detail}]))

(defn timeline
  "Renders the accordion list of sessions for the loaded workflow."
  []
  (let [sessions @(rf/subscribe [:workflows/sessions])
        truncated? @(rf/subscribe [:workflows/truncated?])
        total @(rf/subscribe [:workflows/total])
        expanded-set @(rf/subscribe [:workflows/expanded-set])
        ;; Collect all session ids that are expanded by default
        open-values (->> sessions
                         (filter #(contains? expanded-set (:id %)))
                         (map :id)
                         (into []))
        on-value-change (fn [next-value]
                          (let [next-set (set (js->clj next-value))]
                            (rf/dispatch [:workflows/set-expanded next-set])))]
    [:> Box
     [list-eyebrow (count sessions)]
     (when truncated?
       [:> Box {:class "mb-radix-3"}
        [truncation-banner total (count sessions)]])

     [:> (.-Root Accordion) {:type "multiple"
                             :value (clj->js open-values)
                             :onValueChange on-value-change
                             :className "w-full"}
      (doall
       (for [session sessions]
         ^{:key (:id session)}
         [session-row session]))]]))
